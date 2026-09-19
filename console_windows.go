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
	enableVirtualTerminalProcessing = 0x0004
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

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

// enterRawTerminal 关掉行缓冲/回显，并开启 stdout 的 VT 序列支持。
// 返回还原函数；rawOK 表示可以逐键读取，vtOK 表示可以用 ANSI 重绘菜单。
func enterRawTerminal() (restore func(), rawOK bool, vtOK bool) {
	restore = func() {}

	in := syscall.Stdin
	oldIn, err := getConsoleMode(in)
	if err != nil {
		return restore, false, false
	}
	if err := setConsoleMode(in, oldIn&^(enableLineInput|enableEchoInput|enableProcessedInput)); err != nil {
		return restore, false, false
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
