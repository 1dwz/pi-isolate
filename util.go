package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var colorEnabled = true

// ANSI 控制序列（只在 vtOK 时使用）。
const (
	ansiClearLine      = "\x1b[2K"
	ansiClearToEnd     = "\x1b[J"
	ansiClearScreen    = "\x1b[2J"
	ansiHome           = "\x1b[H"
	ansiHideCursor     = "\x1b[?25l"
	ansiShowCursor     = "\x1b[?25h"
	ansiEnterAltScreen = "\x1b[?1049h"
	ansiLeaveAltScreen = "\x1b[?1049l"
)

// paint 给文本套 ANSI 颜色；未启用时原样返回。
func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string    { return paint("1", s) }
func dim(s string) string     { return paint("2", s) }
func cyan(s string) string    { return paint("36", s) }
func green(s string) string   { return paint("32", s) }
func yellow(s string) string  { return paint("33", s) }
func red(s string) string     { return paint("31", s) }
func inverse(s string) string { return paint("7", s) }

// newLogger 返回一个输出函数。
// 交互菜单占着屏幕时（键控模式）把日志写进文件，避免和菜单互相刷屏；
// 其余场景直接写 stderr，不污染 pi 的 stdout。
func newLogger(quiet, toFile bool) func(string, ...any) {
	if quiet {
		return func(string, ...any) {}
	}
	if !toFile {
		return func(f string, a ...any) {
			fmt.Fprintf(os.Stderr, f+"\n", a...)
		}
	}
	path := logFilePath()
	return func(f string, a ...any) {
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer fh.Close()
		fmt.Fprintf(fh, "[%s] %s\n", timestamp(), fmt.Sprintf(f, a...))
	}
}

// logFilePath 返回启动器日志文件路径（放在 profile 根目录，便于排查）。
func logFilePath() string {
	base := defaultRoot()
	if pd := os.Getenv("LOCALAPPDATA"); pd != "" {
		base = filepath.Join(pd, appName)
		if err := os.MkdirAll(base, 0o755); err != nil {
			base = defaultRoot()
		}
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return filepath.Join(os.TempDir(), appName+".log")
	}
	return filepath.Join(base, appName+".log")
}

// wrapPath 在路径过长时保持终端显示整齐（仅用于展示，不改变实际路径）。
func wrapPath(p string, max int) string {
	if len(p) <= max {
		return p
	}
	return "..." + p[len(p)-(max-3):]
}

// shortAgentDir 把全局 agent 目录显示成 ~/.pi/agent 的形式。
func shortAgentDir(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~\\" + rel
		}
	}
	return p
}
