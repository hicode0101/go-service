package goservice

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger 是组件内部与业务共用的日志接口。默认实现输出到 stderr，
// 服务模式（systemd journal、launchd 重定向、daemon 日志文件、Windows
// 事件日志桥接）下由 App 自动挂接合适的输出端。业务可通过
// WithLogger 注入自己的实现。
type Logger interface {
	Debug(args ...any)
	Debugf(format string, args ...any)
	Info(args ...any)
	Infof(format string, args ...any)
	Warn(args ...any)
	Warnf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
}

// 日志级别。
const (
	LevelDebug = iota
	LevelInfo
	LevelWarn
	LevelError
)

// consoleLogger 输出端统一的日志基础实现，带级别与时间戳。
type consoleLogger struct {
	mu    sync.Mutex
	out   io.Writer
	level int
	quiet bool // 丢弃全部输出（Windows 服务模式无控制台时使用）
}

// NewConsoleLogger 创建输出到 stderr 的日志器，level 为最低输出级别。
func NewConsoleLogger(level int) Logger {
	return &consoleLogger{out: os.Stderr, level: level}
}

// QuietLogger 返回丢弃全部输出的日志器（供 Windows 服务模式挂接
// 事件日志前使用）。
func QuietLogger() Logger {
	return &consoleLogger{out: io.Discard, level: LevelError + 1, quiet: true}
}

func (c *consoleLogger) logf(level int, prefix, format string, args ...any) {
	if c.quiet || level < c.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprintf(c.out, "%s [%s] %s\n", ts, prefix, msg)
}

func (c *consoleLogger) Debug(args ...any)              { c.logf(LevelDebug, "DEBUG", "%s", fmt.Sprint(args...)) }
func (c *consoleLogger) Debugf(format string, a ...any) { c.logf(LevelDebug, "DEBUG", format, a...) }
func (c *consoleLogger) Info(args ...any)               { c.logf(LevelInfo, "INFO", "%s", fmt.Sprint(args...)) }
func (c *consoleLogger) Infof(format string, a ...any)  { c.logf(LevelInfo, "INFO", format, a...) }
func (c *consoleLogger) Warn(args ...any)               { c.logf(LevelWarn, "WARN", "%s", fmt.Sprint(args...)) }
func (c *consoleLogger) Warnf(format string, a ...any)  { c.logf(LevelWarn, "WARN", format, a...) }
func (c *consoleLogger) Error(args ...any)              { c.logf(LevelError, "ERROR", "%s", fmt.Sprint(args...)) }
func (c *consoleLogger) Errorf(format string, a ...any) { c.logf(LevelError, "ERROR", format, a...) }

// fileLogger 在 stderr 之外追加按天滚动的日志文件，用于 daemon
// 后台运行与服务模式下捕获输出。目录不可用时自动降级为纯 stderr。
type fileLogger struct {
	*consoleLogger
	dir  string
	name string

	mu       sync.Mutex
	day      string
	file     *os.File
	combined io.Writer
}

// NewFileLogger 创建写文件（按天滚动）+ stderr 的双写日志器。
func NewFileLogger(dir, name string, level int) Logger {
	base := &consoleLogger{out: os.Stderr, level: level}
	if dir == "" {
		return base
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		base.Warnf("create log dir %s: %v", dir, err)
		return base
	}
	return &fileLogger{
		consoleLogger: base,
		dir:           dir,
		name:          name,
	}
}

// writer 返回当前输出端：stderr + 当天日志文件的 MultiWriter。
func (f *fileLogger) writer() io.Writer {
	f.mu.Lock()
	defer f.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	if f.file == nil || f.day != day {
		if f.file != nil {
			_ = f.file.Close()
			f.file = nil
		}
		path := filepath.Join(f.dir, fmt.Sprintf("%s.%s.log", f.name, day))
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			f.file = file
			f.combined = io.MultiWriter(f.consoleLogger.out, file)
		} else {
			f.combined = f.consoleLogger.out
		}
		f.day = day
	}
	if f.combined == nil {
		return f.consoleLogger.out
	}
	return f.combined
}

func (f *fileLogger) logf(level int, prefix, format string, args ...any) {
	if f.quiet || level < f.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	_, _ = fmt.Fprintf(f.writer(), "%s [%s] %s\n", ts, prefix, msg)
}

func (f *fileLogger) Debug(args ...any)              { f.logf(LevelDebug, "DEBUG", "%s", fmt.Sprint(args...)) }
func (f *fileLogger) Debugf(format string, a ...any) { f.logf(LevelDebug, "DEBUG", format, a...) }
func (f *fileLogger) Info(args ...any)               { f.logf(LevelInfo, "INFO", "%s", fmt.Sprint(args...)) }
func (f *fileLogger) Infof(format string, a ...any)  { f.logf(LevelInfo, "INFO", format, a...) }
func (f *fileLogger) Warn(args ...any)               { f.logf(LevelWarn, "WARN", "%s", fmt.Sprint(args...)) }
func (f *fileLogger) Warnf(format string, a ...any)  { f.logf(LevelWarn, "WARN", format, a...) }
func (f *fileLogger) Error(args ...any)              { f.logf(LevelError, "ERROR", "%s", fmt.Sprint(args...)) }
func (f *fileLogger) Errorf(format string, a ...any) { f.logf(LevelError, "ERROR", format, a...) }
