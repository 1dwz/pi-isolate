//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	enableProcessedInput            = 0x0001
	enableLineInput                 = 0x0002
	enableEchoInput                 = 0x0004
	enableVirtualTerminalProcessing = 0x0004 // 输出侧
	enableVirtualTerminalInput      = 0x0200 // 输入侧（让按键以 VT 序列交付）
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	procGetConsoleInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

func getConsoleMode(h syscall.Handle) (uint32, error) {
	var mode uint32
	r, _, err := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return 0, err
	}
	return mode, nil
}

func setConsoleMode(h syscall.Handle, mode uint32) error {
	r, _, err := procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	if r == 0 {
		return err
	}
	return nil
}

// enterRawTerminal 关掉行缓冲/回显（否则按键要等回车），并开启 stdout 的 VT 序列支持
// （不开的话备用屏/清行等 CSI 序列会被当普通字符打印出来）。
//
// 返回 restore（务必在退出前调用）、rawOK（能否逐键读取）、vtOK（能否用 ANSI 绘制）。
func enterRawTerminal() (restore func(), rawOK bool, vtOK bool) {
	restore = func() {}

	in := syscall.Stdin
	oldIn, err := getConsoleMode(in)
	if err != nil {
		return restore, false, false
	}
	// 关键：清 LINE/ECHO/PROCESSED 三个位，按键即刻可得。
	rawIn := oldIn &^ (enableLineInput | enableEchoInput | enableProcessedInput)
	if err := setConsoleMode(in, rawIn); err != nil {
		return restore, false, false
	}
	// 尽力开启 VT 输入：开启后方向键以 "\x1b[A" 这类统一序列交付。
	// 失败无所谓——未开启时方向键走低位扫描码，两种格式解析器都认。
	if m, err := getConsoleMode(in); err == nil {
		_ = setConsoleMode(in, m|enableVirtualTerminalInput)
	}
	restoreIn := func() { _ = setConsoleMode(in, oldIn) }

	out := syscall.Stdout
	oldOut, err := getConsoleMode(out)
	if err != nil {
		return restoreIn, true, false
	}
	if err := setConsoleMode(out, oldOut|enableVirtualTerminalProcessing); err != nil {
		return restoreIn, true, false
	}
	return func() {
		restoreIn()
		_ = setConsoleMode(out, oldOut)
	}, true, true
}

// terminalSize 返回可见窗口的宽高（不是缓冲区大小——缓冲区通常远大于窗口）。
func terminalSize() (width, height int, ok bool) {
	out := syscall.Stdout
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleInfo.Call(uintptr(out), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, 0, false
	}
	w := int(info.Window.Right-info.Window.Left) + 1
	h := int(info.Window.Bottom-info.Window.Top) + 1
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
