//go:build !darwin

package systray

import real "github.com/energye/systray"

// MenuItem 是托盘菜单项。
//
// 非 macOS 直接沿用 energye/systray：Linux 是纯 Go（D-Bus），
// Windows 也是纯 Go。macOS 的实现见 systray_darwin.go ——
// energye 的 darwin 后端是 Objective-C，交叉编译必须带 Apple SDK，
// 云端 Linux 构建机上编不出来。
type MenuItem = real.MenuItem

// Run 启动系统托盘并阻塞到退出。
func Run(onReady, onExit func()) { real.Run(onReady, onExit) }

// Quit 请求退出托盘消息循环。
func Quit() { real.Quit() }

// Failed 在非 macOS 上恒为 false。
//
// Windows / Linux 的失败表现为 Run 立刻返回或 panic，由托盘包自己捕获。
func Failed() bool { return false }

// SetIcon 设置托盘图标。
func SetIcon(iconBytes []byte) { real.SetIcon(iconBytes) }

// SetTitle 设置托盘标题。
func SetTitle(title string) { real.SetTitle(title) }

// SetTooltip 设置托盘提示。
func SetTooltip(tooltip string) { real.SetTooltip(tooltip) }

// AddMenuItem 添加一个菜单项。
func AddMenuItem(title, tooltip string) *MenuItem {
	return real.AddMenuItem(title, tooltip)
}

// AddMenuItemCheckbox 添加一个可勾选菜单项。
func AddMenuItemCheckbox(title, tooltip string, checked bool) *MenuItem {
	return real.AddMenuItemCheckbox(title, tooltip, checked)
}

// AddSeparator 添加一条分隔线。
func AddSeparator() { real.AddSeparator() }
