//go:build !windows

package main

import "os"

// enterRawTerminal 在非 Windows 平台不做特殊处理，直接走逐行读取的回退菜单。
func enterRawTerminal() (restore func(), rawOK bool, vtOK bool) {
	return func() {}, false, false
}

// terminalSize 在非 Windows 平台返回失败，调用方使用 80x24 默认值。
func terminalSize() (width, height int, ok bool) {
	return 0, 0, false
}

func init() { _ = os.Stdout }
