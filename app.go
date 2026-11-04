package goservice

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

// App 是组件的高层封装：一个 App 对应一个可注册为系统服务的程序。
// 它负责：
//   - 标准执行参数分发（init/run/start/stop/restart/install/uninstall/status 等）；
//   - 任务生命周期调度（Configure → Init → Run → Stop）；
//   - 服务模式自动检测（Windows SCM 回调 / POSIX 前台运行 + 信号）；
//   - 平台控制命令转发（install/uninstall/start/stop/restart/status）。
//
// 典型用法见包文档与本目录 example/demo。
type App struct {
	cfg         Config
	task        Task
	logger      Logger
	stopTimeout time.Duration
	progName    string
}

// Option 是 App 的构造选项。
type Option func(*App)

// WithName 设置服务名（必填），用于服务管理器注册与文件名生成。
func WithName(name string) Option { return func(a *App) { a.cfg.Name = name } }

// WithDisplayName 设置展示名（允许空格）。
func WithDisplayName(name string) Option { return func(a *App) { a.cfg.DisplayName = name } }

// WithDescription 设置服务描述。
func WithDescription(desc string) Option { return func(a *App) { a.cfg.Description = desc } }

// WithUserName 设置服务运行账户。
func WithUserName(user string) Option { return func(a *App) { a.cfg.UserName = user } }

// WithArguments 设置 run 命令的附加参数（注册服务时也会写入启动参数）。
func WithArguments(args []string) Option { return func(a *App) { a.cfg.Arguments = args } }

// WithDependencies 设置服务依赖。
func WithDependencies(deps []string) Option { return func(a *App) { a.cfg.Dependencies = deps } }

// WithWorkingDirectory 设置服务进程工作目录。
func WithWorkingDirectory(dir string) Option { return func(a *App) { a.cfg.WorkingDirectory = dir } }

// WithExecutable 指定服务可执行文件路径；为空时取当前进程。
func WithExecutable(path string) Option { return func(a *App) { a.cfg.Executable = path } }

// WithEnvVars 设置注入服务进程的环境变量。
func WithEnvVars(env map[string]string) Option { return func(a *App) { a.cfg.EnvVars = env } }

// WithOptions 设置平台特定选项（键值含义见 Config.Options 注释）。
func WithOptions(opts map[string]any) Option { return func(a *App) { a.cfg.Options = opts } }

// WithTask 设置业务任务（必填）。
func WithTask(t Task) Option { return func(a *App) { a.task = t } }

// WithLogger 注入自定义日志器。
func WithLogger(l Logger) Option { return func(a *App) { a.logger = l } }

// WithStopTimeout 设置优雅退出超时：ctx 取消后任务在该时长内未返回，
// 进程将强制退出。默认 10s。
func WithStopTimeout(d time.Duration) Option { return func(a *App) { a.stopTimeout = d } }

// New 创建 App 并完成配置校验。Name 与 Task 必填。
func New(opts ...Option) (*App, error) {
	a := &App{stopTimeout: 10 * time.Second}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	if a.cfg.Name == "" {
		return nil, ErrNameRequired
	}
	if a.task == nil {
		return nil, ErrTaskRequired
	}
	if a.cfg.DisplayName == "" {
		a.cfg.DisplayName = a.cfg.Name
	}
	if a.logger == nil {
		a.logger = NewConsoleLogger(LevelInfo)
	}
	if a.progName == "" {
		a.progName = executableName(&a.cfg)
	}
	// 允许任务在启动前读取最终配置与日志器。
	if c, ok := a.task.(Configurable); ok {
		c.Configure(a)
	}
	return a, nil
}

// Config 返回服务配置（只读视图，修改请通过 Option）。
func (a *App) Config() *Config { return &a.cfg }

// Logger 返回组件日志器。
func (a *App) Logger() Logger { return a.logger }

// StopTimeout 返回优雅退出超时。
func (a *App) StopTimeout() time.Duration { return a.stopTimeout }

// PlatformName 返回当前平台标识（解析失败返回 "unknown"）。
func (a *App) PlatformName() string { return PlatformName() }

// Run 是程序主入口：先自动检测服务模式（Windows SCM 调起时进入回调），
// 再按标准执行参数分发命令。args 传 os.Args[1:]。
// 无参数时等价于 run（直接前台运行）。
func (a *App) Run(args []string) error {
	// Windows 服务管理器调起：进入 SCM 回调，命令行参数无关。
	if served, err := a.runAsService(); served {
		return err
	}

	if len(args) == 0 {
		return a.cmdRun(nil)
	}
	switch args[0] {
	case CmdInit:
		return a.cmdInit()
	case CmdRun:
		return a.cmdRun(args[1:])
	case CmdStart:
		return a.cmdControl("start")
	case CmdStop:
		return a.cmdControl("stop")
	case CmdRestart:
		return a.cmdControl("restart")
	case CmdInstall:
		return a.cmdInstall()
	case CmdUninstall:
		return a.cmdUninstall()
	case CmdStatus:
		return a.cmdStatus()
	case CmdDaemon:
		return a.cmdDaemon(args[1:])
	case CmdVersion, "-v", "--version":
		fmt.Printf("%s version %s (%s)\n", a.progName, Version, PlatformName())
		return nil
	case CmdHelp, "-h", "--help":
		a.PrintUsage(os.Stdout)
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		a.PrintUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// PrintUsage 输出标准执行参数说明。
func (a *App) PrintUsage(w *os.File) {
	fmt.Fprintf(w, "Usage: %s <command>\n\nCommands:\n", a.progName)
	fmt.Fprint(w, `  init        初始化运行环境（目录、默认配置、用户 Init 回调）
  run         前台运行服务（默认命令，可省略；POSIX 支持 -d 后台运行）
  start       启动已注册的系统服务
  stop        停止系统服务
  restart     重启系统服务
  install     注册为系统服务（需要管理员/root 权限）
  uninstall   卸载系统服务（需要管理员/root 权限）
  status      查看服务状态
  version     显示版本信息
  help        显示本帮助
`)
}

// cmdInit 执行初始化（Init 的触发场景之一）：创建工作/日志目录，
// 随后调用任务的 Init 回调（任务实现了 Initializer 接口时）。
// 要求幂等，可重复执行。
func (a *App) cmdInit() error {
	if err := a.runInit(); err != nil {
		return err
	}
	a.logger.Infof("init completed (platform: %s)", PlatformName())
	return nil
}

// runInit 默认初始化动作：确保工作目录与日志目录存在，并调用任务
// 的 Init 回调。供 init 与 install 命令复用，要求幂等。
func (a *App) runInit() error {
	if err := a.prepareDirs(); err != nil {
		return err
	}
	if ini, ok := a.task.(Initializer); ok {
		if err := ini.Init(a); err != nil {
			return fmt.Errorf("init task: %w", err)
		}
	}
	return nil
}

// prepareDirs 默认初始化动作：确保工作目录与日志目录存在。
func (a *App) prepareDirs() error {
	for _, dir := range []string{a.cfg.WorkingDirectory, a.logDir()} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// cmdRun 前台运行任务。支持 -d/--daemonize（POSIX）转为后台守护进程。
func (a *App) cmdRun(args []string) error {
	for _, arg := range args {
		if arg == "-d" || arg == "--daemonize" {
			return a.daemonStart()
		}
		if arg == "-h" || arg == "--help" {
			a.PrintUsage(os.Stdout)
			return nil
		}
	}
	return a.runForeground()
}

// runForeground 以信号感知的 context 运行任务，并施加优雅退出超时。
func (a *App) runForeground() error {
	ctx := a.signalContext()

	done := make(chan error, 1)
	go func() { done <- a.runTask(ctx) }()

	// 优雅退出看门狗：ctx 取消后任务超时未返回则强制退出，
	// 避免忽略 ctx 的任务卡死服务管理器的停止流程。
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-stopWatch:
			case <-time.After(a.stopTimeout):
				a.logger.Errorf("task did not stop within %s, forcing exit", a.stopTimeout)
				os.Exit(1)
			}
		case <-stopWatch:
		}
	}()

	err := <-done
	close(stopWatch)
	return err
}

// runTask 调度完整任务生命周期：Run → Stop。
// 注意：用户的 Initializer.Init 仅在显式执行 init 命令时调用
// （见 cmdInit），此处不做启动前初始化。
func (a *App) runTask(ctx context.Context) error {
	err := a.task.Run(ctx)
	if err != nil && ctx.Err() != nil {
		// 因停止信号退出时，任务错误视为附带信息而非失败。
		a.logger.Warnf("task stopped with: %v", err)
		err = nil
	}
	if hook, ok := a.task.(StopHook); ok {
		if stopErr := hook.Stop(); stopErr != nil {
			a.logger.Errorf("stop hook: %v", stopErr)
		}
	}
	// daemon 子进程退出时清理 PID 文件。
	a.cleanupDaemonPID()
	return err
}

// signalContext 返回在收到中断/终止信号时取消的 context。
func (a *App) signalContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), a.stopSignals()...)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx
}

// cmdControl 转发控制命令到当前平台。
func (a *App) cmdControl(action string) error {
	p, err := platformFor()
	if err != nil {
		return err
	}
	switch action {
	case "start":
		err = p.Start(&a.cfg)
	case "stop":
		err = p.Stop(&a.cfg)
	case "restart":
		err = p.Restart(&a.cfg)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", action, a.cfg.Name, err)
	}
	a.logger.Infof("%s %s: ok (platform: %s)", action, a.cfg.Name, p.Name())
	return nil
}

// cmdInstall 注册系统服务（Init 的触发场景之二）：注册前先执行一次
// 初始化（目录准备 + 任务 Init 回调），确保服务运行所依赖的目录/
// 配置随部署就绪。需要管理员/root 权限。
func (a *App) cmdInstall() error {
	p, err := platformFor()
	if err != nil {
		return err
	}
	// 安装前执行一次初始化（幂等），再注册服务。
	if err := a.runInit(); err != nil {
		return err
	}
	if err := p.Install(&a.cfg); err != nil {
		if isPermissionError(err) {
			return fmt.Errorf("install %s: %w (需要管理员/root 权限)", a.cfg.Name, err)
		}
		return fmt.Errorf("install %s: %w", a.cfg.Name, err)
	}
	a.logger.Infof("installed %s on %s", a.cfg.Name, p.Name())
	return nil
}

// cmdUninstall 卸载系统服务。
func (a *App) cmdUninstall() error {
	p, err := platformFor()
	if err != nil {
		return err
	}
	if err := p.Uninstall(&a.cfg); err != nil {
		if isPermissionError(err) {
			return fmt.Errorf("uninstall %s: %w (需要管理员/root 权限)", a.cfg.Name, err)
		}
		return fmt.Errorf("uninstall %s: %w", a.cfg.Name, err)
	}
	a.logger.Infof("uninstalled %s from %s", a.cfg.Name, p.Name())
	return nil
}

// cmdStatus 查询并打印服务状态。
func (a *App) cmdStatus() error {
	p, err := platformFor()
	if err != nil {
		return err
	}
	st, err := p.Status(&a.cfg)
	if err != nil {
		fmt.Printf("platform: %s\nservice: %s\nstatus:  %s (%v)\n", p.Name(), a.cfg.Name, st, err)
		return nil
	}
	fmt.Printf("platform: %s\nservice: %s\nstatus:  %s\n", p.Name(), a.cfg.Name, st)
	return nil
}

// cmdDaemon 分发隐藏的内部 daemon 子命令（供 SysV/rc.d init 脚本薄封装
// 调用，也可供运维直接使用）：daemon start|stop|restart|status。
func (a *App) cmdDaemon(args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: %s daemon <start|stop|restart|status>\n", a.progName)
		return fmt.Errorf("daemon: missing action")
	}
	switch args[0] {
	case "start":
		return a.daemonStart()
	case "stop":
		return a.daemonStop()
	case "restart":
		return a.daemonRestart()
	case "status":
		return a.daemonStatusCmd()
	default:
		return fmt.Errorf("daemon: unknown action %q", args[0])
	}
}

// isPermissionError 判断错误是否与权限相关，用于给出可操作的提示。
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Access is denied") ||
		strings.Contains(msg, "access denied") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "operation not permitted")
}
