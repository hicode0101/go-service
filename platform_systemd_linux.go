//go:build linux

package goservice

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// systemd 平台：渲染 unit 文件写入 /etc/systemd/system（或用户目录），
// 通过 systemctl 控制服务。服务进程以 Type=simple 前台运行，
// 收到 SIGTERM 后优雅退出。

const (
	optSystemdUnit  = "SystemdUnit"  // 自定义 unit 文件模板
	optLimitNOFILE  = "LimitNOFILE"  // 最大打开文件数（-1 不设置）
	optRestartRule  = "Restart"      // 重启策略（默认 on-failure）
	optRestartSec   = "RestartSec"   // 重启延迟（默认 5s）
	optStopTimeout  = "TimeoutStopSec"
)

func init() {
	Register(&Definition{
		Priority: 10,
		Detect:   isSystemd,
		New:      func() Platform { return &systemdPlatform{} },
	})
}

// isSystemd 检测当前系统是否由 systemd 管理。
func isSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	if fileExists("/run/systemd/system") {
		return true
	}
	if data, err := os.ReadFile("/proc/1/comm"); err == nil {
		return strings.TrimSpace(string(data)) == "systemd"
	}
	return false
}

type systemdPlatform struct{}

func (systemdPlatform) Name() string        { return "linux-systemd" }
func (systemdPlatform) Detect() bool        { return isSystemd() }
func (systemdPlatform) Interactive() bool   { return os.Getenv("INVOCATION_ID") == "" }

// unitName 返回 unit 文件名。
func (s *systemdPlatform) unitName(cfg *Config) string { return cfg.Name + ".service" }

// unitPath 返回 unit 文件路径；UserService 选项为 true 时安装为用户服务。
func (s *systemdPlatform) unitPath(cfg *Config) (string, error) {
	if cfg.optionBool("UserService", false) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "systemd", "user", s.unitName(cfg)), nil
	}
	return filepath.Join("/etc/systemd/system", s.unitName(cfg)), nil
}

// systemdUnitTemplate 默认 unit 文件模板。
var systemdUnitTemplate = template.Must(template.New("systemd").Parse(`[Unit]
Description={{.Description}}
{{if .AfterLines}}{{range .AfterLines}}{{.}}
{{end}}{{end}}
[Service]
Type=simple
ExecStart={{.ExePath}} run{{.ArgsSuffix}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory}}
{{end -}}
{{if .User}}User={{.User}}
{{end -}}
{{if .EnvLines}}{{range .EnvLines}}{{.}}
{{end}}{{end -}}
{{if .LimitNOFILE}}LimitNOFILE={{.LimitNOFILE}}
{{end -}}
Restart={{.RestartRule}}
RestartSec={{.RestartSec}}
KillSignal=SIGTERM
TimeoutStopSec={{.TimeoutStopSec}}
[Install]
WantedBy=multi-user.target
`))

type systemdUnitData struct {
	Description      string
	AfterLines       []string
	ExePath          string
	ArgsSuffix       string
	WorkingDirectory string
	User             string
	EnvLines         []string
	LimitNOFILE      string
	RestartRule      string
	RestartSec       string
	TimeoutStopSec   string
}

// renderUnit 渲染 unit 文件内容。自定义模板通过
// Options["SystemdUnit"] 注入。
func (s *systemdPlatform) renderUnit(cfg *Config) ([]byte, error) {
	exePath, err := executablePath(cfg)
	if err != nil {
		return nil, err
	}

	after := []string{"After=network.target"}
	for _, dep := range cfg.Dependencies {
		switch {
		case strings.HasPrefix(dep, "After="), strings.HasPrefix(dep, "Requires="),
			strings.HasPrefix(dep, "Wants="), strings.HasPrefix(dep, "Before="),
			strings.HasPrefix(dep, "Conflicts="):
			// 原样附加到 [Unit] 段。
		default:
			after = append(after, "After="+dep)
			continue
		}
		after = append(after, dep)
	}

	argsSuffix := ""
	for _, arg := range cfg.Arguments {
		argsSuffix += " " + arg
	}

	envLines := make([]string, 0, len(cfg.EnvVars))
	for k, v := range cfg.EnvVars {
		envLines = append(envLines, "Environment=\""+k+"="+v+"\"")
	}

	restart := cfg.optionString(optRestartRule, "on-failure")
	restartSec := cfg.optionString(optRestartSec, "5s")
	stopSec := cfg.optionString(optStopTimeout, "30s")
	limit := ""
	if n := cfg.optionInt(optLimitNOFILE, -1); n > 0 {
		limit = fmt.Sprintf("%d", n)
	}

	data := systemdUnitData{
		Description:      cfg.Description,
		AfterLines:       after,
		ExePath:          exePath,
		ArgsSuffix:       argsSuffix,
		WorkingDirectory: cfg.WorkingDirectory,
		User:             cfg.UserName,
		EnvLines:         envLines,
		LimitNOFILE:      limit,
		RestartRule:      restart,
		RestartSec:       restartSec,
		TimeoutStopSec:   stopSec,
	}

	tmpl := systemdUnitTemplate
	if custom := cfg.optionString(optSystemdUnit, ""); custom != "" {
		tmpl, err = template.New("systemd-custom").Parse(custom)
		if err != nil {
			return nil, fmt.Errorf("parse custom systemd unit template: %w", err)
		}
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *systemdPlatform) Install(cfg *Config) error {
	unitPath, err := s.unitPath(cfg)
	if err != nil {
		return err
	}
	if fileExists(unitPath) {
		return ErrAlreadyInstalled
	}
	content, err := s.renderUnit(cfg)
	if err != nil {
		return err
	}
	if err := writeFile(unitPath, content, 0o644); err != nil {
		return err
	}
	// 重载配置并设置开机自启（尽力而为，失败不回滚文件）。
	_ = runCmd("systemctl", "daemon-reload")
	if cfg.optionBool("UserService", false) {
		_ = runCmd("systemctl", "--user", "enable", cfg.Name)
	} else {
		_ = runCmd("systemctl", "enable", cfg.Name)
	}
	return nil
}

func (s *systemdPlatform) Uninstall(cfg *Config) error {
	unitPath, err := s.unitPath(cfg)
	if err != nil {
		return err
	}
	if !fileExists(unitPath) {
		return ErrNotInstalled
	}
	// 先停止并禁用，忽略未运行/未启用导致的错误。
	_ = runCmd("systemctl", "stop", cfg.Name)
	_ = runCmd("systemctl", "disable", cfg.Name)
	if err := os.Remove(unitPath); err != nil {
		return err
	}
	_ = runCmd("systemctl", "daemon-reload")
	return nil
}

func (s *systemdPlatform) systemctlArgs(cfg *Config) []string {
	if cfg.optionBool("UserService", false) {
		return []string{"--user"}
	}
	return nil
}

func (s *systemdPlatform) Start(cfg *Config) error {
	args := append(s.systemctlArgs(cfg), "start", cfg.Name)
	return runCmd("systemctl", args...)
}

func (s *systemdPlatform) Stop(cfg *Config) error {
	args := append(s.systemctlArgs(cfg), "stop", cfg.Name)
	return runCmd("systemctl", args...)
}

func (s *systemdPlatform) Restart(cfg *Config) error {
	args := append(s.systemctlArgs(cfg), "restart", cfg.Name)
	return runCmd("systemctl", args...)
}

func (s *systemdPlatform) Status(cfg *Config) (Status, error) {
	// unit 文件不存在视为未安装。
	if unitPath, perr := s.unitPath(cfg); perr == nil && !fileExists(unitPath) {
		return StatusUnknown, ErrNotInstalled
	}
	args := append(s.systemctlArgs(cfg), "is-active", cfg.Name)
	out, _ := runCmdOutput("systemctl", args...)
	state := strings.TrimSpace(out)
	switch state {
	case "active":
		return StatusRunning, nil
	case "inactive", "deactivating":
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}
