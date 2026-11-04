//go:build windows

package goservice

import (
	"fmt"

	"golang.org/x/sys/windows/svc"
)

// runAsService 检测当前进程是否被 Windows 服务控制管理器（SCM）调起；
// 是则进入服务回调模式并阻塞至服务停止。
func (a *App) runAsService() (bool, error) {
	is, err := svc.IsWindowsService()
	if err != nil {
		return true, fmt.Errorf("detect service mode: %w", err)
	}
	if !is {
		return false, nil
	}
	// 服务模式无控制台，日志切换到 Windows 事件日志。
	a.useEventLog()
	return true, runWindowsService(a)
}
