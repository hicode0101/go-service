// Package goservice 提供一个开箱即用的跨平台系统服务组件。
//
// 一个编译产物同时具备三种能力：
//
//   - 直接运行：前台运行业务任务（默认命令），Ctrl+C / SIGTERM 优雅退出；
//   - 注册服务：install 注册为系统服务（Windows SCM、systemd、SysV、
//     OpenRC、Upstart、macOS launchd、FreeBSD rc.d）；
//   - 卸载服务：uninstall 移除系统服务。
//
// 标准执行参数：
//
//	myapp init        初始化运行环境（目录、默认配置、用户 Init 回调）
//	myapp run         前台运行（默认命令，可省略）
//	myapp start       启动已注册的系统服务
//	myapp stop        停止系统服务
//	myapp restart     重启系统服务
//	myapp install     注册为系统服务（需要管理员 / root 权限）
//	myapp uninstall   卸载系统服务（需要管理员 / root 权限）
//	myapp status      查看服务状态
//	myapp version     显示版本信息
//	myapp help        显示帮助
//
// 用法示例：
//
//	package main
//
//	import (
//		"context"
//		"fmt"
//		"os"
//
//		goservice "github.com/hicode0101/go-service"
//	)
//
//	type worker struct{}
//
//	func (w *worker) Run(ctx context.Context) error {
//		for {
//			select {
//			case <-ctx.Done(): // 收到停止信号，尽快返回
//				return nil
//			}
//		}
//	}
//
//	func main() {
//		app, err := goservice.New(
//			goservice.WithName("myapp"),
//			goservice.WithDisplayName("My Application"),
//			goservice.WithDescription("demo service"),
//			goservice.WithTask(&worker{}),
//		)
//		if err != nil {
//			fmt.Fprintln(os.Stderr, err)
//			os.Exit(1)
//		}
//		if err := app.Run(os.Args[1:]); err != nil {
//			fmt.Fprintln(os.Stderr, err)
//			os.Exit(1)
//		}
//	}
package goservice

import "errors"

// Status 表示系统服务的运行状态。
type Status int

const (
	// StatusUnknown 无法确定状态（未安装、探测失败等）。
	StatusUnknown Status = iota
	// StatusRunning 服务正在运行。
	StatusRunning
	// StatusStopped 服务已安装但处于停止状态。
	StatusStopped
)

// String 返回状态的文字描述。
func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// 标准执行参数（命令）。扩展命令只需在 App 分发层注册。
const (
	CmdInit      = "init"
	CmdRun       = "run"
	CmdStart     = "start"
	CmdStop      = "stop"
	CmdRestart   = "restart"
	CmdInstall   = "install"
	CmdUninstall = "uninstall"
	CmdStatus    = "status"
	CmdDaemon    = "daemon" // 隐藏内部命令：daemon start|stop|restart|status
	CmdVersion   = "version"
	CmdHelp      = "help"
)

// Config 描述一个系统服务的注册信息，Name 必填。
type Config struct {
	// Name 服务名（必填）。各平台用于生成 unit/init.d/plist 等文件名，
	// 建议使用小写字母、数字与连字符，不要包含空格。
	Name string
	// DisplayName 展示名，允许空格。为空时取 Name。
	DisplayName string
	// Description 服务描述。
	Description string
	// UserName 服务运行账户（Windows: ServiceStartName；systemd: User）。
	UserName string
	// Arguments 传递给 run 命令的附加参数。
	Arguments []string
	// Dependencies 服务依赖。
	//   systemd: 追加到 [Unit] 段（如 "After=network.target"）；
	//   Windows: SCM 依赖服务名列表。
	Dependencies []string
	// WorkingDirectory 工作目录（服务进程启动时的初始目录）。
	WorkingDirectory string
	// Executable 可执行文件路径；为空时取当前进程可执行文件。
	Executable string
	// EnvVars 注入服务进程的环境变量。
	EnvVars map[string]string
	// Options 平台特定选项，键值见各平台文档。常见键：
	//
	//   Windows:
	//     "StartType"    string ("automatic"|"manual"|"disabled")  默认 automatic
	//     "DelayedAutoStart" bool                                  延迟自启动
	//     "OnFailure"    string ("restart"|"reboot"|"noaction")    失败恢复动作
	//     "OnFailureDelayDuration" string ("1s")                    恢复延迟
	//     "OnFailureResetPeriod" int (10)                            失败计数重置周期(秒)
	//     "Password"     string                                      账户密码
	//   POSIX (systemd/launchd/daemon):
	//     "LogDirectory" string  日志目录（默认 /var/log）
	//     "RunAtLoad"    bool    launchd 安装后立即启动（默认 true）
	//     "KeepAlive"    bool    launchd 崩溃自动拉起（默认 true）
	//   systemd:
	//     "LimitNOFILE"  int     最大打开文件数（-1 表示不设置）
	//     "Restart"      string  重启策略（默认 on-failure）
	Options map[string]any
}

// Option 返回平台选项中指定键的值，不存在时返回零值与 false。
func (c *Config) Option(key string) (any, bool) {
	if c.Options == nil {
		return nil, false
	}
	v, ok := c.Options[key]
	return v, ok
}

// optionString 返回字符串型平台选项，缺省时返回 def。
func (c *Config) optionString(key, def string) string {
	if v, ok := c.Option(key); ok {
		if s, is := v.(string); is {
			return s
		}
	}
	return def
}

// optionBool 返回布尔型平台选项，缺省时返回 def。
func (c *Config) optionBool(key string, def bool) bool {
	if v, ok := c.Option(key); ok {
		if b, is := v.(bool); is {
			return b
		}
	}
	return def
}

// optionInt 返回整型平台选项，缺省时返回 def。
func (c *Config) optionInt(key string, def int) int {
	if v, ok := c.Option(key); ok {
		if n, is := v.(int); is {
			return n
		}
	}
	return def
}

// optionStringSlice 返回字符串切片型平台选项，缺省时返回 nil。
func (c *Config) optionStringSlice(key string) []string {
	if v, ok := c.Option(key); ok {
		switch s := v.(type) {
		case []string:
			return s
		case string:
			return []string{s}
		}
	}
	return nil
}

// 组件级错误。
var (
	// ErrNameRequired Config.Name 为空时返回。
	ErrNameRequired = errors.New("goservice: config name is required")
	// ErrTaskRequired 未设置 Task 时返回。
	ErrTaskRequired = errors.New("goservice: task is required")
	// ErrNoPlatform 未检测到可用的服务管理平台时返回。
	ErrNoPlatform = errors.New("goservice: no service platform detected")
	// ErrNotInstalled 服务未安装时返回。
	ErrNotInstalled = errors.New("goservice: service is not installed")
	// ErrAlreadyInstalled 服务已安装时返回。
	ErrAlreadyInstalled = errors.New("goservice: service is already installed")
	// ErrAlreadyRunning 服务已在运行时返回。
	ErrAlreadyRunning = errors.New("goservice: service is already running")
	// ErrNotRunning 服务未运行时返回。
	ErrNotRunning = errors.New("goservice: service is not running")
)

// PlatformName 返回当前检测到的平台描述（如 "linux-systemd"、"windows-scm"）。
// 未检测到时返回 "unknown"。
func PlatformName() string {
	if p, err := currentPlatform(); err == nil {
		return p.Name()
	}
	return "unknown"
}

// Interactive 返回当前进程是否运行在交互终端（true）而非服务管理器（false）。
func Interactive() bool {
	if p, err := currentPlatform(); err == nil {
		return p.Interactive()
	}
	return true
}
