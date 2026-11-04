//go:build windows

package goservice

import (
	"errors"
	"os"
	"path/filepath"
)

// Windows 无 POSIX 信号模型，仅响应 Ctrl+C（服务模式下走 SCM 回调）。
func (a *App) stopSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

// logDir 返回日志目录：优先 Options["LogDirectory"]，否则回退可执行文件目录。
func (a *App) logDir() string {
	if s := a.cfg.optionString("LogDirectory", ""); s != "" {
		return s
	}
	if exe, err := executablePath(&a.cfg); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

// errDaemonUnsupported Windows 没有 POSIX fork/setsid，不支持进程内
// 守护化；请使用 install + start 交给服务控制管理器托管。
var errDaemonUnsupported = errors.New("daemon mode is not supported on windows; use 'install' + 'start' instead")

func (a *App) daemonStart() error    { return errDaemonUnsupported }
func (a *App) daemonStop() error     { return errDaemonUnsupported }
func (a *App) daemonRestart() error  { return errDaemonUnsupported }
func (a *App) daemonStatusCmd() error {
	return errDaemonUnsupported
}
func (a *App) daemonStatus() (Status, error) { return StatusUnknown, errDaemonUnsupported }

// cleanupDaemonPID Windows 下无 daemon 子进程，无需清理。
func (a *App) cleanupDaemonPID() {}
