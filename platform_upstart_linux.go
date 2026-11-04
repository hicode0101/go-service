//go:build linux

package goservice

import (
	"bytes"
	"os"
	"path/filepath"
	"text/template"
)

// Upstart 平台（Ubuntu 14.04 等旧系统）：生成 /etc/init/<name>.conf
// 任务描述文件，由 initctl 控制。

func init() {
	Register(&Definition{
		Priority: 20,
		Detect:   isUpstart,
		New:      func() Platform { return &upstartPlatform{} },
	})
}

// isUpstart 检测 Upstart。
func isUpstart() bool {
	if fileExists("/sbin/initctl") && fileExists("/etc/init") {
		return true
	}
	return false
}

type upstartPlatform struct{}

func (upstartPlatform) Name() string      { return "linux-upstart" }
func (upstartPlatform) Detect() bool      { return isUpstart() }
func (upstartPlatform) Interactive() bool { return os.Getenv("UPSTART_JOB") == "" }

func (s *upstartPlatform) confPath(cfg *Config) string {
	return filepath.Join("/etc/init", cfg.Name+".conf")
}

var upstartConfTemplate = template.Must(template.New("upstart").Parse(`# {{.Description}}
description "{{.Description}}"
{{if .User}}setuid {{.User}}
{{end -}}
start on filesystem or runlevel [2345]
stop on runlevel [!2345]

respawn
respawn limit 10 5
kill signal TERM
kill timeout {{.KillTimeout}}

{{if .EnvLines}}{{range .EnvLines}}{{.}}
{{end}}{{end -}}
{{if .WorkingDirectory}}chdir {{.WorkingDirectory}}
{{end -}}
{{if .ConsoleLog}}console log
{{else}}console none
{{end -}}
exec {{.ExePath}} run{{.ArgsSuffix}}
`))

func (s *upstartPlatform) renderConf(cfg *Config) ([]byte, error) {
	exePath, err := executablePath(cfg)
	if err != nil {
		return nil, err
	}
	envLines := make([]string, 0, len(cfg.EnvVars))
	for k, v := range cfg.EnvVars {
		envLines = append(envLines, "env \""+k+"="+v+"\"")
	}
	// 系统日志目录可写时用 console log，否则丢弃输出。
	consoleLog := cfg.optionString("LogDirectory", "") != ""
	data := map[string]any{
		"Description":     firstNonEmpty(cfg.Description, cfg.DisplayName),
		"ExePath":         exePath,
		"ArgsSuffix":      joinArgs(cfg.Arguments),
		"User":            cfg.UserName,
		"EnvLines":        envLines,
		"WorkingDirectory": cfg.WorkingDirectory,
		"KillTimeout":     cfg.optionInt("TimeoutStopSec", 30),
		"ConsoleLog":      consoleLog,
	}
	var buf bytes.Buffer
	if err := upstartConfTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *upstartPlatform) Install(cfg *Config) error {
	path := s.confPath(cfg)
	if fileExists(path) {
		return ErrAlreadyInstalled
	}
	content, err := s.renderConf(cfg)
	if err != nil {
		return err
	}
	if err := writeFile(path, content, 0o644); err != nil {
		return err
	}
	return nil
}

func (s *upstartPlatform) Uninstall(cfg *Config) error {
	path := s.confPath(cfg)
	if !fileExists(path) {
		return ErrNotInstalled
	}
	_ = runCmd("initctl", "stop", cfg.Name)
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func (s *upstartPlatform) Start(cfg *Config) error   { return runCmd("initctl", "start", cfg.Name) }
func (s *upstartPlatform) Stop(cfg *Config) error    { return runCmd("initctl", "stop", cfg.Name) }
func (s *upstartPlatform) Restart(cfg *Config) error { return runCmd("initctl", "restart", cfg.Name) }

func (s *upstartPlatform) Status(cfg *Config) (Status, error) {
	err := runCmd("initctl", "status", cfg.Name)
	if err == nil {
		return StatusRunning, nil
	}
	return StatusStopped, nil
}
