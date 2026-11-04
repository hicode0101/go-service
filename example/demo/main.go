// demo 演示如何基于 go-service 构建一个可注册为系统服务的程序。
//
// 构建：go build -o demo ./example/demo
//
// 直接运行：      ./demo            （等价于 ./demo run）
// 后台运行：      ./demo run -d
// 初始化：        ./demo init
// 注册为服务：    ./demo install    （需要管理员/root 权限）
// 启动/停止：     ./demo start / ./demo stop
// 查询状态：      ./demo status
// 卸载服务：      ./demo uninstall
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	goservice "github.com/hicode0101/go-service"
)

// demoTask 业务任务：只需实现 Run(ctx) 最小接口。
type demoTask struct {
	logger goservice.Logger
}

// Configure 可选：启动前拿到 App（含配置与日志器）。
func (t *demoTask) Configure(a *goservice.App) {
	t.logger = a.Logger()
}

// Init 可选：在两种场景被调用——
//   1. 显式执行 `demo init` 命令；
//   2. 执行 `demo install` 时，注册服务前先执行一次。
//
// 幂等约定：同一操作执行一次与执行多次，结果完全相同——重复执行
// 不产生副作用累积。init 命令可能被重复执行（人工重跑、部署脚本
// 重复调用），组件不拦截重复调用，因此 Init 实现必须满足幂等。
// 常用套路：
//
//	建目录用 MkdirAll（已存在时静默跳过）；
//	写默认文件先 Stat 判断，不存在才写，避免覆盖用户修改；
//	数据库用 IF NOT EXISTS / ON CONFLICT DO NOTHING。
func (t *demoTask) Init(a *goservice.App) error {
	// 1. 创建数据目录：MkdirAll 对已存在目录静默成功 —— 幂等。
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// 2. 生成默认配置：已存在则跳过，不会覆盖用户修改 —— 幂等。
	cfgPath := filepath.Join(dataDir, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
			return fmt.Errorf("write default config: %w", err)
		}
		a.Logger().Infof("demo init: default config written to %s", cfgPath)
	} else {
		a.Logger().Infof("demo init: config already exists at %s, skipped", cfgPath)
	}

	a.Logger().Infof("demo init: working dir=%s", a.Config().WorkingDirectory)
	return nil
}

// defaultConfig 首次 init 时生成的演示配置。
const defaultConfig = `{
  "listen": ":8080",
  "workers": 4
}
`

// Run 业务主体：阻塞运行，监听 ctx.Done() 响应停止信号。
func (t *demoTask) Run(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			t.logger.Info("demo task: context canceled, exiting")
			return nil
		case <-ticker.C:
			n++
			t.logger.Infof("demo task: working ... (%d)", n)
		}
	}
}

// Stop 可选：ctx 取消后的最后清理机会。
func (t *demoTask) Stop() error {
	t.logger.Info("demo task: stop hook")
	return nil
}

func main() {
	app, err := goservice.New(
		goservice.WithName("go-service-demo"),
		goservice.WithDisplayName("Go Service Demo"),
		goservice.WithDescription("演示 go-service 跨平台服务组件"),
		goservice.WithTask(&demoTask{}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := app.Run(os.Args[1:]); err != nil {
		app.Logger().Errorf("exit: %v", err)
		os.Exit(1)
	}
}
