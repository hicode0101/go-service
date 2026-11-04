//go:build linux

package goservice

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

// OpenRC 平台（Alpine / Gentoo / Artix 等）：生成 openrc-run 风格的
// /etc/init.d/<name> 脚本，由 rc-service 控制；command_background=yes
// 时由 openrc 自行后台化并管理 PID 文件。

func init() {
	Register(&Definition{
		Priority: 30,
		Detect:   isOpenRC,
		New:      func() Platform { return &openRCPlatform{} },
	})
}

// isOpenRC 检测 OpenRC。
func isOpenRC() bool {
	if fileExists("/run/openrc") {
		return true
	}
	if _, err := exec.LookPath("rc-service"); err == nil {
		return true
	}
	if fileExists("/sbin/openrc-run") {
		return true
	}
	return false
}

type openRCPlatform struct{}

func (openRCPlatform) Name() string      { return "linux-openrc" }
func (openRCPlatform) Detect() bool      { return isOpenRC() }
func (openRCPlatform) Interactive() bool { return os.Getenv("RC_SERVICE") == "" }

func (s *openRCPlatform) scriptPath(cfg *Config) string {
	return filepath.Join("/etc/init.d", cfg.Name)
}

var openRCScriptTemplate = template.Must(template.New("openrc").Parse(`#!/sbin/openrc-run

name="{{.Name}}"
description="{{.Description}}"
command="{{.ExePath}}"
command_args="run{{.ArgsSuffix}}"
command_background="yes"
pidfile="{{.PIDFile}}"
directory="{{.WorkingDirectory}}"
output_log="{{.LogDir}}/{{.Name}}.log"
error_log="{{.LogDir}}/{{.Name}}.err"
{{if .User}}user="{{.User}}"
{{end -}}
depend() {
{{range .Depends}}	{{.}}
{{end}}}
`))

func (s *openRCPlatform) renderScript(cfg *Config) ([]byte, error) {
	exePath, err := executablePath(cfg)
	if err != nil {
		return nil, err
	}
	depends := []string{"net"}
	for _, dep := range cfg.Dependencies {
		depends = append(depends, dep)
	}
	logDir := cfg.optionString("LogDirectory", defaultLogDir())
	data := map[string]any{
		"Name":            cfg.Name,
		"Description":     firstNonEmpty(cfg.Description, cfg.DisplayName),
		"ExePath":         exePath,
		"ArgsSuffix":      joinArgs(cfg.Arguments),
		"PIDFile":         fmt.Sprintf("/run/%s.pid", cfg.Name),
		"WorkingDirectory": cfg.WorkingDirectory,
		"LogDir":          logDir,
		"User":            cfg.UserName,
		"Depends":         depends,
	}
	var buf bytes.Buffer
	if err := openRCScriptTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *openRCPlatform) Install(cfg *Config) error {
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
	// 加入默认运行级别的开机自启。
	_ = runCmd("rc-update", "add", cfg.Name, "default")
	return nil
}

func (s *openRCPlatform) Uninstall(cfg *Config) error {
	path := s.scriptPath(cfg)
	if !fileExists(path) {
		return ErrNotInstalled
	}
	_ = runCmd("rc-service", cfg.Name, "stop")
	_ = runCmd("rc-update", "del", cfg.Name, "default")
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func (s *openRCPlatform) Start(cfg *Config) error   { return runCmd("rc-service", cfg.Name, "start") }
func (s *openRCPlatform) Stop(cfg *Config) error    { return runCmd("rc-service", cfg.Name, "stop") }
func (s *openRCPlatform) Restart(cfg *Config) error { return runCmd("rc-service", cfg.Name, "restart") }

func (s *openRCPlatform) Status(cfg *Config) (Status, error) {
	err := runCmd("rc-service", cfg.Name, "status")
	if err == nil {
		return StatusRunning, nil
	}
	return StatusStopped, nil
}
