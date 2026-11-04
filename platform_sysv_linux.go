//go:build linux

package goservice

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

// SysV init.d 平台：生成一个"薄封装"的 /etc/init.d/<name> 脚本，
// 脚本只做一件事——把 start/stop/restart/status 代理给本程序的
// daemon 子命令。全部逻辑集中在 Go 程序内部，便于升级与排查。

func init() {
	Register(&Definition{
		Priority: 100, // 兜底：systemd/upstart/openrc 检测失败时启用
		Detect:   func() bool { return true },
		New:      func() Platform { return &sysvPlatform{} },
	})
}

type sysvPlatform struct{}

func (sysvPlatform) Name() string      { return "linux-sysv" }
func (sysvPlatform) Detect() bool      { return true }
func (sysvPlatform) Interactive() bool { return os.Getenv(daemonEnv) != "1" }

// initScriptPath 返回 init 脚本路径。
func (sysvPlatform) initScriptPath(cfg *Config) string {
	return filepath.Join("/etc/init.d", cfg.Name)
}

var sysvScriptTemplate = template.Must(template.New("sysv").Parse(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          {{.Name}}
# Required-Start:    $network $remote_fs
# Required-Stop:     $network $remote_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.Description}}
### END INIT INFO

DAEMON="{{.ExePath}}"
NAME="{{.Name}}"

case "$1" in
  start)
    "$DAEMON" daemon start
    ;;
  stop)
    "$DAEMON" daemon stop || exit 0
    ;;
  restart)
    "$DAEMON" daemon restart
    ;;
  status)
    "$DAEMON" daemon status || exit 3
    ;;
  force-reload)
    "$DAEMON" daemon restart
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|force-reload|status}"
    exit 1
    ;;
esac
`))

func (s *sysvPlatform) renderScript(cfg *Config) ([]byte, error) {
	exePath, err := executablePath(cfg)
	if err != nil {
		return nil, err
	}
	data := map[string]string{
		"Name":        cfg.Name,
		"Description": firstNonEmpty(cfg.Description, cfg.DisplayName),
		"ExePath":     exePath,
	}
	var buf bytes.Buffer
	if err := sysvScriptTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *sysvPlatform) Install(cfg *Config) error {
	scriptPath := s.initScriptPath(cfg)
	if fileExists(scriptPath) {
		return ErrAlreadyInstalled
	}
	content, err := s.renderScript(cfg)
	if err != nil {
		return err
	}
	if err := writeFile(scriptPath, content, 0o755); err != nil {
		return err
	}
	// 注册开机自启：Debian 系与 RedHat 系，尽力而为。
	_ = runCmd("update-rc.d", cfg.Name, "defaults")
	_ = runCmd("chkconfig", "--add", cfg.Name)
	return nil
}

func (s *sysvPlatform) Uninstall(cfg *Config) error {
	scriptPath := s.initScriptPath(cfg)
	if !fileExists(scriptPath) {
		return ErrNotInstalled
	}
	_ = runCmd("update-rc.d", "-f", cfg.Name, "remove")
	_ = runCmd("chkconfig", "--del", cfg.Name)
	if err := os.Remove(scriptPath); err != nil {
		return err
	}
	return nil
}

func (s *sysvPlatform) Start(cfg *Config) error   { return runCmd(s.initScriptPath(cfg), "start") }
func (s *sysvPlatform) Stop(cfg *Config) error    { return runCmd(s.initScriptPath(cfg), "stop") }
func (s *sysvPlatform) Restart(cfg *Config) error { return runCmd(s.initScriptPath(cfg), "restart") }

func (s *sysvPlatform) Status(cfg *Config) (Status, error) {
	// 脚本 status 代理到 daemon status：0=运行, 3=停止（LSB）。
	err := runCmd(s.initScriptPath(cfg), "status")
	if err == nil {
		return StatusRunning, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
		return StatusStopped, nil
	}
	return StatusUnknown, nil
}
