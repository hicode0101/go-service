//go:build !windows

package goservice

// 非 Windows 平台没有服务管理器回调模型：
// systemd / launchd 以前台进程（Type=simple / ProgramArguments）直接拉起，
// SysV 等经由 daemon 子命令后台化后同样是普通前台进程，因此统一走
// runForeground 的信号处理路径，无需特殊服务模式。

func (a *App) runAsService() (bool, error) { return false, nil }
