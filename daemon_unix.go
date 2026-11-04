//go:build !windows

package goservice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// POSIX 平台的信号集合：Ctrl+C 与 kill 默认信号。
func (a *App) stopSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// ---- 后台守护进程化（daemon 子命令 / run -d）----

const daemonEnv = "GO_SERVICE_DAEMON"

// logDir 返回日志目录：优先 Options["LogDirectory"]；root 用 /var/log；
// 否则退回工作目录。
func (a *App) logDir() string {
	if s := a.cfg.optionString("LogDirectory", ""); s != "" {
		return s
	}
	if os.Geteuid() == 0 {
		return defaultLogDir()
	}
	if a.cfg.WorkingDirectory != "" {
		return a.cfg.WorkingDirectory
	}
	return "."
}

// pidFile 返回 PID 文件路径：优先 Options["PIDFile"]；root 写 /var/run，
// 普通用户写临时目录。
func (a *App) pidFile() string {
	if s := a.cfg.optionString("PIDFile", ""); s != "" {
		return s
	}
	name := a.cfg.Name + ".pid"
	if os.Geteuid() == 0 {
		return filepath.Join("/var/run", name)
	}
	return filepath.Join(os.TempDir(), "go-service-"+name)
}

// daemonStart 将当前程序以守护进程方式启动：setsid 分离子进程运行
// run 命令，输出重定向到日志文件，PID 写入 PID 文件。
func (a *App) daemonStart() error {
	if st, _ := a.daemonStatus(); st == StatusRunning {
		return ErrAlreadyRunning
	}
	exe, err := executablePath(&a.cfg)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	args := append([]string{CmdRun}, a.cfg.Arguments...)
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // 脱离终端与进程组
	cmd.Dir = a.cfg.WorkingDirectory
	cmd.Env = a.daemonEnv(os.Environ())

	// 日志目录可能尚未创建（init 未执行过），兜底创建。
	if err := os.MkdirAll(a.logDir(), 0o755); err != nil {
		return fmt.Errorf("create log dir %s: %w", a.logDir(), err)
	}
	logPath := filepath.Join(a.logDir(), a.cfg.Name+".daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log %s: %w", logPath, err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release() // 交由系统托管，父进程立即退出

	if err := a.writePID(pid); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	fmt.Printf("started %s in background (pid %d)\nlog: %s\npid file: %s\n",
		a.cfg.Name, pid, logPath, a.pidFile())
	return nil
}

// daemonEnv 构造守护进程子进程环境：继承当前环境 + 服务环境变量 +
// 守护进程标记。
func (a *App) daemonEnv(base []string) []string {
	env := base
	for k, v := range a.cfg.EnvVars {
		env = append(env, k+"="+v)
	}
	return append(env, daemonEnv+"=1")
}

// daemonStop 停止后台守护进程：先 SIGTERM，超过优雅退出超时后 SIGKILL。
func (a *App) daemonStop() error {
	pid, ok := a.readPID()
	if !ok || !pidAlive(pid) {
		a.removePID()
		return ErrNotRunning
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(a.stopTimeout)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			a.removePID()
			fmt.Printf("stopped %s (pid %d)\n", a.cfg.Name, pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 优雅退出超时，强制终止。
	_ = syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	a.removePID()
	fmt.Printf("killed %s (pid %d) after timeout\n", a.cfg.Name, pid)
	return nil
}

// daemonRestart 重启守护进程：未运行时直接启动。
func (a *App) daemonRestart() error {
	if err := a.daemonStop(); err != nil && err != ErrNotRunning {
		return err
	}
	return a.daemonStart()
}

// daemonStatusCmd 输出守护进程状态。停止时返回 ErrNotRunning，
// 供 init 脚本映射为 LSB 退出码。
func (a *App) daemonStatusCmd() error {
	pid, ok := a.readPID()
	if ok && pidAlive(pid) {
		fmt.Printf("%s (pid %d): running\n", a.cfg.Name, pid)
		return nil
	}
	fmt.Printf("%s: stopped\n", a.cfg.Name)
	return ErrNotRunning
}

// daemonStatus 返回守护进程状态（供内部判断）。
func (a *App) daemonStatus() (Status, error) {
	pid, ok := a.readPID()
	if ok && pidAlive(pid) {
		return StatusRunning, nil
	}
	return StatusStopped, nil
}

// cleanupDaemonPID 守护进程子进程退出时清理自身 PID 文件。
func (a *App) cleanupDaemonPID() {
	if os.Getenv(daemonEnv) != "1" {
		return
	}
	if pid, ok := a.readPID(); ok && pid == os.Getpid() {
		a.removePID()
	}
}

// readPID 读取 PID 文件。
func (a *App) readPID() (int, bool) {
	data, err := os.ReadFile(a.pidFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// writePID 写入 PID 文件。
func (a *App) writePID(pid int) error {
	return writeFile(a.pidFile(), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// removePID 删除 PID 文件（忽略错误）。
func (a *App) removePID() { _ = os.Remove(a.pidFile()) }

// pidAlive 判断进程是否存活：kill(pid, 0) 成功或返回 EPERM 均视为存活。
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
