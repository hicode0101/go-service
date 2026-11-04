package goservice

import "context"

// Task 是业务任务必须实现的最小接口。
//
// Run 由组件在以下场景调用：
//   - 前台运行（run 命令 / 默认命令）；
//   - 作为系统服务运行（Windows SCM 回调、systemd/launchd 直接拉起、
//     SysV 守护进程化子进程）。
//
// Run 必须阻塞执行业务主体，并通过监听 ctx.Done() 在收到取消信号后
// 尽快返回（建议在数秒内）。返回 nil 表示正常退出。
//
//	func (t *myTask) Run(ctx context.Context) error {
//		for {
//			select {
//			case <-ctx.Done():
//				return nil // 停止
//			default:
//				// ... 业务逻辑 ...
//			}
//		}
//	}
type Task interface {
	Run(ctx context.Context) error
}

// TaskFunc 将普通函数适配为 Task。
type TaskFunc func(ctx context.Context) error

// Run 实现 Task 接口。
func (f TaskFunc) Run(ctx context.Context) error { return f(ctx) }

// Initializer 可选扩展接口：任务实现它后，Init 在两种场景被调用：
//   - 显式执行 init 命令；
//   - 执行 install 命令时，注册服务前先执行一次（确保服务运行所依赖
//     的目录/配置随部署就绪）。
//
// Init 要求幂等，可重复执行；普通 run 与服务启动不会触发它。
type Initializer interface {
	Init(a *App) error
}

// StopHook 可选扩展接口：任务实现它后，当 Run 因 ctx 取消而结束前，
// 组件会调用 Stop 给业务最后一次清理机会（刷新缓冲、关闭连接等）。
type StopHook interface {
	// Stop 在 ctx 已取消后调用，应快速返回，避免阻塞退出流程。
	Stop() error
}

// Configurable 可选扩展接口：任务实现它后，组件在构造完成后会把
// 运行时 App 实例（含 Config 与 Logger）回传给任务，便于业务读取
// 服务配置或写日志。
type Configurable interface {
	Configure(a *App)
}
