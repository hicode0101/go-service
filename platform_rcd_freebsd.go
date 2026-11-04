//go:build freebsd

package goservice

import (
	"bytes"
	"os"
	"path/filepath"
	"text/template"
)

// FreeBSD rc.d 平台：生成 /usr/local/etc/rc.d/<name> 脚本，
// 由 service(8) 控制。借助系统自带 daemon(8) 以 -r（崩溃自动重启）
// 与 -p（PID 文件）参数托管服务进程。

func init() {
	Register(&Definition{
		Priority: 100, // 兜底平台
		Detect:   func() bool { return true },
		New:      func() Platform { return &rcdPlatform{} },
	})
}

type rcdPlatform struct{}

func (rcdPlatform) Name() string      { return "freebsd-rcd" }
func (rcdPlatform) Detect() bool      { return true }
func (rcdPlatform) Interactive() bool { return os.Getenv("RC_PID") == "" }

func (s *rcdPlatform) scriptPath(cfg *Config) string {
	return filepath.Join("/usr/local/etc/rc.d", cfg.Name)
}

var rcdScriptTemplate = template.Must(template.New("rcd").Parse(`#!/bin/sh
# PROVIDE: {{.Name}}
# REQUIRE: DAEMON NETWORKING SERVERS
# KEYWORD: shutdown

. /etc/rc.subr

name="{{.Name}}"
rcvar="{{.Name}}_enable"
desc="{{.Description}}"
command="/usr/sbin/daemon"
daemon_args="-r -f -p {{.PIDFile}} {{.ExePath}} run{{.ArgsSuffix}}"
pidfile="{{.PIDFile}}"
start_cmd="{{.Name}}_start"
stop_cmd="{{.Name}}_stop"
status_cmd="{{.Name}}_status"

load_rc_config $name

${name}_start() {
	check_startmsgs && echo "Starting ${name}."
	daemon $daemon_args
}

${name}_stop() {
	if [ -f "{{.PIDFile}}" ]; then
		kill -TERM $(cat "{{.PIDFile}}") 2>/dev/null || true
		kill -0 $(cat "{{.PIDFile}}") 2>/dev/null && sleep 2
		rm -f "{{.PIDFile}}"
	fi
}

${name}_status() {
	if [ -f "{{.PIDFile}}" ] && kill -0 $(cat "{{.PIDFile}}") 2>/dev/null; then
		echo "${name} is running as pid $(cat "{{.PIDFile}}")."
		return 0
	fi
	echo "${name} is not running."
	return 1
}

run_rc_command "$1"
`))

func (s *rcdPlatform) renderScript(cfg *Config) ([]byte, error) {
	exePath, err := executablePath(cfg)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"Name":        cfg.Name,
		"Description": firstNonEmpty(cfg.Description, cfg.DisplayName),
		"ExePath":     exePath,
		"PIDFile":     cfg.optionString("PIDFile", "/var/run/"+cfg.Name+".pid"),
		"ArgsSuffix":  joinArgs(cfg.Arguments),
	}
	var buf bytes.Buffer
	if err := rcdScriptTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *rcdPlatform) Install(cfg *Config) error {
	path := s.scriptPath(cfg)
	if fileExists(path) {
		return ErrAlreadyInstalled
	}
	content, err := s.renderScript(cfg)
	if err != nil {
		return err
	}
	if err := writeFile(path, content, 0o755); err != nil {
		return err
	}
	// 提示：在 /etc/rc.conf 中加入 name_enable="YES" 以开机自启。
	return nil
}

func (s *rcdPlatform) Uninstall(cfg *Config) error {
	path := s.scriptPath(cfg)
	if !fileExists(path) {
		return ErrNotInstalled
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func (s *rcdPlatform) Start(cfg *Config) error   { return runCmd(s.scriptPath(cfg), "start") }
func (s *rcdPlatform) Stop(cfg *Config) error    { return runCmd(s.scriptPath(cfg), "stop") }
func (s *rcdPlatform) Restart(cfg *Config) error { return runCmd(s.scriptPath(cfg), "restart") }

func (s *rcdPlatform) Status(cfg *Config) (Status, error) {
	err := runCmd(s.scriptPath(cfg), "status")
	if err == nil {
		return StatusRunning, nil
	}
	return StatusStopped, nil
}
