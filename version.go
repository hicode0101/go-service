package goservice

import "runtime/debug"

// 组件版本号。
const Version = "1.0.0"

// BuildInfo 返回构建信息（Go 版本与模块版本，尽力而为）。
func BuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return Version + " (module " + info.Main.Version + ")"
	}
	return Version
}
