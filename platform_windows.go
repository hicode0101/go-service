//go:build windows

package goservice

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// Windows SCM 平台：注册时经 svc/mgr 创建服务（含恢复策略、环境变量、
// 事件日志源）；运行时通过 svc.Run 挂接 SCM 回调，Stop/Shutdown 请求
// 转换为 context 取消，任务按优雅退出超时返回。

// Windows 平台选项键。
const (
	optStartType           = "StartType"            // automatic | manual | disabled
	optDelayedAutoStart    = "DelayedAutoStart"     // 延迟自启动
	optOnFailure           = "OnFailure"            // restart | reboot | noaction
	optOnFailureDelay      = "OnFailureDelayDuration"
	optOnFailureResetPeriod = "OnFailureResetPeriod"
	optPassword            = "Password"
	optInteractive         = "Interactive"
)

func init() {
	Register(&Definition{
		Priority: 0,
		Detect:   func() bool { return true },
		New:      func() Platform { return &windowsPlatform{} },
	})
}

type windowsPlatform struct{}

func (windowsPlatform) Name() string { return "windows-scm" }
func (windowsPlatform) Detect() bool { return true }

func (windowsPlatform) Interactive() bool {
	is, err := svc.IsWindowsService()
	if err != nil {
		return true
	}
	return !is
}

// errServiceNotExist 服务不存在（SCM 错误码 1060）。
var errServiceNotExist syscall.Errno = 1060

// ---- 服务模式回调 ----

// winHandler 实现 svc.Handler：桥接 SCM 控制请求与任务 context。
type winHandler struct{ a *App }

func (h *winHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending, WaitHint: uint32(h.a.stopTimeout.Milliseconds())}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.a.runTask(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepts}
	for req := range r {
		switch req.Cmd {
		case svc.Interrogate:
			changes <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending, WaitHint: uint32((h.a.stopTimeout + 5*time.Second).Milliseconds())}
			cancel()
			select {
			case <-done:
			case <-time.After(h.a.stopTimeout + 5*time.Second):
				return false, 2 // 任务未按时退出，返回失败码供 SCM 记录
			}
			return false, 0
		default:
			// 忽略未知请求。
		}
	}
	return false, 0
}

// runWindowsService 进入 SCM 服务回调，阻塞至服务停止。
func runWindowsService(a *App) error {
	return svc.Run(a.cfg.Name, &winHandler{a: a})
}

// ---- 事件日志 ----

// eventLogger 将日志桥接到 Windows 事件日志。
type eventLogger struct{ el *eventlog.Log }

func (l eventLogger) write(level int, msg string) {
	switch level {
	case LevelError:
		_ = l.el.Error(1, msg)
	case LevelWarn:
		_ = l.el.Warning(2, msg)
	default:
		_ = l.el.Info(3, msg)
	}
}
func (l eventLogger) Debug(args ...any)              { l.write(LevelDebug, fmt.Sprint(args...)) }
func (l eventLogger) Debugf(f string, a ...any)      { l.write(LevelDebug, fmt.Sprintf(f, a...)) }
func (l eventLogger) Info(args ...any)               { l.write(LevelInfo, fmt.Sprint(args...)) }
func (l eventLogger) Infof(f string, a ...any)       { l.write(LevelInfo, fmt.Sprintf(f, a...)) }
func (l eventLogger) Warn(args ...any)               { l.write(LevelWarn, fmt.Sprint(args...)) }
func (l eventLogger) Warnf(f string, a ...any)       { l.write(LevelWarn, fmt.Sprintf(f, a...)) }
func (l eventLogger) Error(args ...any)              { l.write(LevelError, fmt.Sprint(args...)) }
func (l eventLogger) Errorf(f string, a ...any)      { l.write(LevelError, fmt.Sprintf(f, a...)) }

// useEventLog 将 App 日志切换为事件日志；源未注册时丢弃输出。
func (a *App) useEventLog() {
	el, err := eventlog.Open(a.cfg.Name)
	if err != nil {
		a.logger = QuietLogger()
		return
	}
	a.logger = eventLogger{el: el}
}

// ---- 服务管理 ----

func connectSCM() (*mgr.Mgr, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect service manager: %w", err)
	}
	return m, nil
}

// openService 打开已注册服务，不存在时返回 errServiceNotExist。
func openService(m *mgr.Mgr, name string) (*mgr.Service, error) {
	s, err := m.OpenService(name)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (windowsPlatform) Install(cfg *Config) error {
	exePath, err := executablePath(cfg)
	if err != nil {
		return err
	}
	m, err := connectSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	if s, err := openService(m, cfg.Name); err == nil {
		s.Close()
		return ErrAlreadyInstalled
	}

	var startType uint32
	switch cfg.optionString(optStartType, "automatic") {
	case "manual":
		startType = mgr.StartManual
	case "disabled":
		startType = mgr.StartDisabled
	default:
		startType = mgr.StartAutomatic
	}

	serviceType := uint32(windows.SERVICE_WIN32_OWN_PROCESS)
	if cfg.optionBool(optInteractive, false) {
		serviceType |= uint32(windows.SERVICE_INTERACTIVE_PROCESS)
	}

	args := append([]string{CmdRun}, cfg.Arguments...)
	s, err := m.CreateService(cfg.Name, exePath, mgr.Config{
		DisplayName:      cfg.DisplayName,
		Description:      cfg.Description,
		StartType:        startType,
		ServiceStartName: cfg.UserName,
		Password:         cfg.optionString(optPassword, ""),
		Dependencies:     cfg.Dependencies,
		DelayedAutoStart: cfg.optionBool(optDelayedAutoStart, false),
		ServiceType:      serviceType,
	}, args...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// 失败恢复动作。
	if onFailure := cfg.optionString(optOnFailure, ""); onFailure != "" {
		var actionType int
		switch onFailure {
		case "reboot":
			actionType = mgr.ComputerReboot
		case "noaction":
			actionType = mgr.NoAction
		default: // restart
			actionType = mgr.ServiceRestart
		}
		delay := 1 * time.Second
		if d, perr := time.ParseDuration(cfg.optionString(optOnFailureDelay, "1s")); perr == nil {
			delay = d
		}
		if rerr := s.SetRecoveryActions([]mgr.RecoveryAction{
			{Type: actionType, Delay: delay},
		}, uint32(cfg.optionInt(optOnFailureResetPeriod, 10))); rerr != nil {
			return fmt.Errorf("set recovery actions: %w", rerr)
		}
	}

	// 服务进程环境变量（SCM 在启动时读取注册表注入）。
	if len(cfg.EnvVars) > 0 {
		k, _, err := registry.CreateKey(
			registry.LOCAL_MACHINE,
			`SYSTEM\CurrentControlSet\Services\`+cfg.Name,
			registry.QUERY_VALUE|registry.SET_VALUE|registry.CREATE_SUB_KEY)
		if err != nil {
			return fmt.Errorf("create env registry key: %w", err)
		}
		envStrings := make([]string, 0, len(cfg.EnvVars))
		for key, val := range cfg.EnvVars {
			envStrings = append(envStrings, key+"="+val)
		}
		err = k.SetStringsValue("Environment", envStrings)
		_ = k.Close()
		if err != nil {
			return fmt.Errorf("set env registry value: %w", err)
		}
	}

	// 注册事件日志源，供服务模式写系统日志。
	_ = eventlog.Install(cfg.Name, exePath, false, uint32(eventlog.Error|eventlog.Warning|eventlog.Info))
	return nil
}

func (windowsPlatform) Uninstall(cfg *Config) error {
	m, err := connectSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := openService(m, cfg.Name)
	if err != nil {
		return ErrNotInstalled
	}
	defer s.Close()

	// 运行中则先请求停止（尽力而为）。
	if st, qerr := s.Query(); qerr == nil && st.State == svc.Running {
		_, _ = s.Control(svc.Stop)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if nst, qerr := s.Query(); qerr == nil && nst.State == svc.Stopped {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	_ = eventlog.Remove(cfg.Name)
	return nil
}

func (windowsPlatform) Start(cfg *Config) error {
	m, err := connectSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := openService(m, cfg.Name)
	if err != nil {
		return ErrNotInstalled
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		if st, qerr := s.Query(); qerr == nil && st.State == svc.Running {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func (windowsPlatform) Stop(cfg *Config) error {
	m, err := connectSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := openService(m, cfg.Name)
	if err != nil {
		return ErrNotInstalled
	}
	defer s.Close()
	st, qerr := s.Query()
	if qerr != nil {
		return fmt.Errorf("query service: %w", qerr)
	}
	if st.State == svc.Stopped {
		return ErrNotRunning
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service: %w", err)
	}
	return nil
}

func (windowsPlatform) Restart(cfg *Config) error {
	m, err := connectSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := openService(m, cfg.Name)
	if err != nil {
		return ErrNotInstalled
	}
	defer s.Close()
	if st, qerr := s.Query(); qerr == nil && st.State == svc.Running {
		if _, serr := s.Control(svc.Stop); serr != nil {
			return fmt.Errorf("stop service: %w", serr)
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if nst, qerr := s.Query(); qerr == nil && nst.State == svc.Stopped {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func (windowsPlatform) Status(cfg *Config) (Status, error) {
	m, err := connectSCM()
	if err != nil {
		return StatusUnknown, err
	}
	defer m.Disconnect()
	s, err := openService(m, cfg.Name)
	if err != nil {
		return StatusUnknown, ErrNotInstalled
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return StatusUnknown, fmt.Errorf("query service: %w", err)
	}
	switch st.State {
	case svc.Running:
		return StatusRunning, nil
	case svc.Stopped:
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}
