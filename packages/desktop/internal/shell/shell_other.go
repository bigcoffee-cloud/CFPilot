//go:build !windows && !linux

package shell

// New 在没有内嵌窗口的平台上明确返回 ErrUnsupported。
//
// macOS 走这里：菜单栏由 internal/systray 用运行时 dlopen 的 AppKit 提供，
// 面板打开系统浏览器。WKWebView 要在编译期链接 Cocoa，云端 Linux 交叉编译做不到。
// 容器和其它尚未支持的平台也走这条回退，不 panic、不假装窗口已打开。
func New(opts Options) (Panel, error) {
	opts.logf("[shell] 当前平台没有内嵌窗口实现，回退系统浏览器")
	return nil, ErrUnsupported
}

// ActivateExisting 在没有窗口实现的平台上恒为 false。
func ActivateExisting(string) bool { return false }
