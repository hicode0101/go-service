package goservice

import (
	"fmt"
	"sort"
	"sync"
)

// Platform 是某个具体服务管理平台（Windows SCM、systemd、sysv、launchd
// 等）需要实现的控制面接口。所有方法只操作"已注册的系统服务"，不负责
// 进程内业务运行（业务运行统一由 App 在前台完成）。
//
// 扩展新平台：实现本接口后，在包初始化时调用 Register 注册即可，
// 注册表按 Priority 从小到大依次 Detect，第一个可用的平台生效。
type Platform interface {
	// Name 平台标识，如 "linux-systemd"、"windows-scm"。
	Name() string
	// Detect 当前运行环境是否为该平台管理的环境。
	Detect() bool
	// Interactive 当前进程是否运行在交互终端（而非服务管理器调起）。
	Interactive() bool

	// Install 注册系统服务。已注册时返回 ErrAlreadyInstalled。
	Install(cfg *Config) error
	// Uninstall 卸载系统服务。未注册时返回 ErrNotInstalled。
	Uninstall(cfg *Config) error

	// Start 通过服务管理器启动服务。
	Start(cfg *Config) error
	// Stop 通过服务管理器停止服务。
	Stop(cfg *Config) error
	// Restart 通过服务管理器重启服务。
	Restart(cfg *Config) error
	// Status 查询服务状态。
	Status(cfg *Config) (Status, error)
}

// Definition 描述一个待注册的平台实现。
type Definition struct {
	// Priority 选择优先级，越小越优先。兜底实现（如 SysV）应给较大值。
	Priority int
	// Detect 环境检测函数；为 nil 时视为恒可用（兜底平台）。
	Detect func() bool
	// New 平台构造器，每次解析成功后调用。
	New func() Platform
}

var (
	registryMu   sync.RWMutex
	registryDefs []*Definition
	currentP     Platform
	currentOnce  sync.Once
)

// Register 注册一个平台实现。同优先级按注册顺序。
func Register(d *Definition) {
	if d == nil || d.New == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registryDefs = append(registryDefs, d)
	sort.SliceStable(registryDefs, func(i, j int) bool {
		return registryDefs[i].Priority < registryDefs[j].Priority
	})
	currentOnce = sync.Once{} // 注册后允许重新解析
}

// Registered 返回已注册平台的名字列表（按优先级排序）。
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registryDefs))
	for _, d := range registryDefs {
		p := d.New()
		names = append(names, p.Name())
	}
	return names
}

// currentPlatform 解析并缓存当前生效的平台：按 Priority 顺序调用
// Detect，第一个命中的实现生效；全部未命中返回 ErrNoPlatform。
func currentPlatform() (Platform, error) {
	registryMu.RLock()
	current := currentP
	registryMu.RUnlock()
	if current != nil {
		return current, nil
	}

	registryMu.RLock()
	defs := make([]*Definition, len(registryDefs))
	copy(defs, registryDefs)
	registryMu.RUnlock()

	for _, d := range defs {
		detect := d.Detect
		if detect == nil {
			detect = func() bool { return true }
		}
		if !detect() {
			continue
		}
		p := d.New()
		registryMu.Lock()
		currentP = p
		registryMu.Unlock()
		return p, nil
	}
	return nil, ErrNoPlatform
}

// resetPlatformCache 仅供测试使用。
func resetPlatformCache() {
	registryMu.Lock()
	currentP = nil
	currentOnce = sync.Once{}
	registryMu.Unlock()
}

// platformFor 返回当前平台；检测失败时返回降级的错误信息。
func platformFor() (Platform, error) {
	p, err := currentPlatform()
	if err != nil {
		return nil, fmt.Errorf("%w (registered: %v)", err, Registered())
	}
	return p, nil
}
