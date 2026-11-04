package goservice

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runCmd 执行外部命令，成功返回 nil；失败时错误信息包含命令与 stderr。
func runCmd(name string, args ...string) error {
	_, err := runCmdOutput(name, args...)
	return err
}

// runCmdOutput 执行外部命令并返回标准输出。
func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// fileExists 判断路径是否存在。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeFile 确保父目录存在后写入文件。
func writeFile(path string, content []byte, perm os.FileMode) error {
	if dir := parentDir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content, perm)
}

// parentDir 返回路径的目录部分（ filepath.Dir 的空安全封装）。
func parentDir(path string) string { return filepath.Dir(path) }

// executablePath 返回服务可执行文件路径：优先 cfg.Executable，
// 否则当前进程可执行文件（解析符号链接）。
func executablePath(cfg *Config) (string, error) {
	if cfg.Executable != "" {
		abs, err := filepath.Abs(cfg.Executable)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// executableName 返回可执行文件基础名（不含扩展名）。
func executableName(cfg *Config) string {
	path, err := executablePath(cfg)
	if err != nil {
		return cfg.Name
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// defaultLogDir 返回平台默认日志目录（POSIX）。
func defaultLogDir() string { return "/var/log" }

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// joinArgs 将参数切片拼接为带空格前缀的字符串。
func joinArgs(args []string) string {
	out := ""
	for _, a := range args {
		out += " " + a
	}
	return out
}
