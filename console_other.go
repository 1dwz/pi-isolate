//go:build !windows

package main

// enterRawTerminal 在非 Windows 平台不做特殊处理，直接走逐行读取的回退菜单。
func enterRawTerminal() (restore func(), rawOK bool, vtOK bool) {
	return func() {}, false, false
}
