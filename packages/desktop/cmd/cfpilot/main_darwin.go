//go:build darwin

package main

import "runtime"

func init() {
	// AppKit 要求 NSApplication 跑在进程启动时的主线程上。
	// Go 在 main 阻塞之后会把这个 goroutine 迁走，菜单栏就起不来。
	// 在任何初始化之前钉住，后面的托盘循环才能留在主线程。
	runtime.LockOSThread()
}
