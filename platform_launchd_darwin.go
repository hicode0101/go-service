//go:build darwin

package goservice

import (
	"bytes"
	"os"
	"path/filepath"
	"text/template"
)

// macOS launchd 平台：root 安装写 /Library/LaunchDaemons（系统级，
// 开机自启），普通用户写 ~/Library/LaunchAgents（用户级）；
// 通过 launchctl load/unload 控制。服务以前台进程运行，
// 由 launchd 负责 KeepAlive 自动拉起。

func init() {
	Register(&Definition{
		Priority: 0,
		Detect:   func() bool { return true },
		New:      func() Platform { return &launchdPlatform{} },
	})
}

type launchdPlatform struct{}

func (launchdPlatform) Name() string      { return "darwin-launchd" }
func (launchdPlatform) Detect() bool      { return true }
func (launchdPlatform) Interactive() bool { return os.Getenv("LAUNCHED") == "" && os.Getppid() != 1 }

// plistPath 返回 plist 文件路径（按安装级别决定目录）。
func (s *launchdPlatform) plistPath(cfg *Config) (string, error) {
	if cfg.optionBool("UserService", false) || os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "LaunchAgents", cfg.Name+".plist"), nil
	}
	return filepath.Join("/Library", "LaunchDaemons", cfg.Name+".plist"), nil
}

var launchdPlistTemplate = template.Must(template.New("launchd").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Name}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.ExePath}}</string>
		<string>run</string>{{range .Args}}
		<string>{{.}}</string>{{end}}
	</array>
	<key>RunAtLoad</key>
	<{{.RunAtLoad}}/>
	<key>KeepAlive</key>
	<{{.KeepAlive}}/>
{{if .WorkingDirectory}}	<key>WorkingDirectory</key>
	<string>{{.WorkingDirectory}}</string>
{{end}}{{if .UserName}}	<key>UserName</key>
	<string>{{.UserName}}</string>
{{end}}{{if .EnvVars}}	<key>EnvironmentVariables</key>
	<dict>
{{range $k, $v := .EnvVars}}		<key>{{$k}}</key>
		<string>{{$v}}</string>
{{end}}	</dict>
{{end}}	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.ErrLogPath}}</string>
</dict>
</plist>
`))

type launchdData struct {
	Name             string
	Description      string
	ExePath          string
	Args             []string
	RunAtLoad        string
	KeepAlive        string
	WorkingDirectory string
	UserName         string
	EnvVars          map[string]string
	LogPath          string
	ErrLogPath       string
}

func (s *launchdPlatform) renderPlist(cfg *Config) ([]byte, error) {
	exePath, err := executablePath(cfg)
	if err != nil {
		return nil, err
	}
	logDir := cfg.optionString("LogDirectory", "/var/log")
	data := launchdData{
		Name:             cfg.Name,
		Description:      cfg.Description,
		ExePath:          exePath,
		Args:             cfg.Arguments,
		RunAtLoad:        boolStr2(cfg.optionBool("RunAtLoad", true)),
		KeepAlive:        boolStr2(cfg.optionBool("KeepAlive", true)),
		WorkingDirectory: cfg.WorkingDirectory,
		UserName:         cfg.UserName,
		EnvVars:          cfg.EnvVars,
		LogPath:          filepath.Join(logDir, cfg.Name+".out.log"),
		ErrLogPath:       filepath.Join(logDir, cfg.Name+".err.log"),
	}
	var buf bytes.Buffer
	if err := launchdPlistTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// boolStr2 布尔值转 XML 字面量。
func boolStr2(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (s *launchdPlatform) Install(cfg *Config) error {
	path, err := s.plistPath(cfg)
	if err != nil {
		return err
	}
	if fileExists(path) {
		return ErrAlreadyInstalled
	}
	content, err := s.renderPlist(cfg)
	if err != nil {
		return err
	}
	if err := writeFile(path, content, 0o644); err != nil {
		return err
	}
	_ = runCmd("launchctl", "load", "-w", path)
	return nil
}

func (s *launchdPlatform) Uninstall(cfg *Config) error {
	path, err := s.plistPath(cfg)
	if err != nil {
		return err
	}
	if !fileExists(path) {
		return ErrNotInstalled
	}
	_ = runCmd("launchctl", "unload", "-w", path)
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func (s *launchdPlatform) Start(cfg *Config) error {
	path, err := s.plistPath(cfg)
	if err != nil {
		return err
	}
	if !fileExists(path) {
		return ErrNotInstalled
	}
	return runCmd("launchctl", "load", "-w", path)
}

func (s *launchdPlatform) Stop(cfg *Config) error {
	path, err := s.plistPath(cfg)
	if err != nil {
		return err
	}
	if !fileExists(path) {
		return ErrNotInstalled
	}
	return runCmd("launchctl", "unload", "-w", path)
}

func (s *launchdPlatform) Restart(cfg *Config) error {
	if err := s.Stop(cfg); err != nil && err != ErrNotInstalled {
		return err
	}
	return s.Start(cfg)
}

func (s *launchdPlatform) Status(cfg *Config) (Status, error) {
	err := runCmd("launchctl", "list", cfg.Name)
	if err == nil {
		return StatusRunning, nil
	}
	if fileExists(filepath.Join("/Library", "LaunchDaemons", cfg.Name+".plist")) {
		return StatusStopped, nil
	}
	// 用户级 plist。
	if home, herr := os.UserHomeDir(); herr == nil &&
		fileExists(filepath.Join(home, "Library", "LaunchAgents", cfg.Name+".plist")) {
		return StatusStopped, nil
	}
	return StatusUnknown, ErrNotInstalled
}
