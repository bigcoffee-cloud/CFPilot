//go:build darwin

// macOS 菜单栏。
//
// energye/systray 的 darwin 后端是 Objective-C，交叉编译要 Apple SDK。
// 这里改用 purego 在运行时 dlopen AppKit：CGO 保持关闭，Linux 构建机也能产出
// Mach-O。菜单的增删改从其它 goroutine 进来时，会派发到主线程再碰 AppKit。
package systray

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	nsVariableStatusItemLength = float64(-1)
	nsControlStateOn           = 1
	nsControlStateOff          = 0
	// NSApplicationActivationPolicyAccessory：只出现在菜单栏，不占 Dock。
	nsActivationPolicyAccessory = 1
)

var (
	failed atomic.Bool

	mu       sync.Mutex
	items    = map[uint32]*MenuItem{}
	nextID   uint32
	quitOnce sync.Once

	nsApp      objc.ID
	statusItem objc.ID
	rootMenu   objc.ID
	target     objc.ID
	iconKeep   []byte

	dispatchAsyncF   func(queue uintptr, ctx unsafe.Pointer, work uintptr)
	mainQueue        func() uintptr
	pthreadMainNP    func() int
	cfRunLoopGetMain func() uintptr
	cfRunLoopWakeUp  func(loop uintptr)
	workCallback     uintptr

	classNSString   objc.Class
	classNSData     objc.Class
	classNSImage    objc.Class
	classNSNumber   objc.Class
	classNSMenu     objc.Class
	classNSMenuItem objc.Class

	selSharedApplication      objc.SEL
	selSetActivationPolicy    objc.SEL
	selRun                    objc.SEL
	selStop                   objc.SEL
	selRetain                 objc.SEL
	selAlloc                  objc.SEL
	selInit                   objc.SEL
	selStringWithUTF8String   objc.SEL
	selDataWithBytesLength    objc.SEL
	selInitWithData           objc.SEL
	selSystemStatusBar        objc.SEL
	selStatusItemWithLength   objc.SEL
	selButton                 objc.SEL
	selSetImage               objc.SEL
	selSetTitle               objc.SEL
	selSetToolTip             objc.SEL
	selSetMenu                objc.SEL
	selAddItem                objc.SEL
	selSetAutoenablesItems    objc.SEL
	selSeparatorItem          objc.SEL
	selSetTarget              objc.SEL
	selSetAction              objc.SEL
	selSetRepresentedObject   objc.SEL
	selRepresentedObject      objc.SEL
	selNumberWithUnsignedLong objc.SEL
	selUnsignedLongValue      objc.SEL
	selSetEnabled             objc.SEL
	selSetHidden              objc.SEL
	selSetState               objc.SEL
	selSetSubmenu             objc.SEL
	selHandle                 objc.SEL
)

// MenuItem 是菜单栏里的一项。字段只在持有 mu 或主线程时改。
type MenuItem struct {
	id       uint32
	title    string
	tooltip  string
	disabled bool
	checked  bool
	hidden   bool
	parent   *MenuItem
	click    func()
	ns       objc.ID
	sub      objc.ID
}

// Failed 报告菜单栏是否没能建起来。
func Failed() bool { return failed.Load() }

// Run 在主线程拉起菜单栏并阻塞，直到 Quit。
func Run(onReady, onExit func()) {
	if err := boot(onReady); err != nil {
		log.Printf("[systray] macOS 菜单栏初始化失败：%v", err)
		failed.Store(true)
		return
	}
	if onExit != nil {
		onExit()
	}
}

// Quit 让 [NSApp run] 返回。
func Quit() {
	quitOnce.Do(func() {
		onMain(func() {
			if nsApp != 0 {
				nsApp.Send(selStop, nsApp)
			}
			if cfRunLoopWakeUp != nil && cfRunLoopGetMain != nil {
				cfRunLoopWakeUp(cfRunLoopGetMain())
			}
		})
	})
}

// SetIcon 用 PNG 字节设置菜单栏图标。
func SetIcon(iconBytes []byte) {
	if len(iconBytes) == 0 {
		return
	}
	kept := append([]byte(nil), iconBytes...)
	onMain(func() {
		iconKeep = kept
		if statusItem == 0 {
			return
		}
		data := sendClass(classNSData, selDataWithBytesLength, unsafe.Pointer(&iconKeep[0]), uintptr(len(iconKeep)))
		runtime.KeepAlive(iconKeep)
		if data == 0 {
			return
		}
		img := sendClass(classNSImage, selAlloc).Send(selInitWithData, data)
		if img == 0 {
			return
		}
		button := statusItem.Send(selButton)
		if button != 0 {
			button.Send(selSetImage, img)
		}
	})
}

// SetTitle 设置菜单栏按钮旁边的文字。
func SetTitle(title string) {
	onMain(func() {
		button := statusButton()
		if button != 0 {
			button.Send(selSetTitle, nsString(title))
		}
	})
}

// SetTooltip 设置菜单栏按钮的提示。
func SetTooltip(tooltip string) {
	onMain(func() {
		button := statusButton()
		if button != 0 {
			button.Send(selSetToolTip, nsString(tooltip))
		}
	})
}

// AddMenuItem 添加一个根菜单项。
func AddMenuItem(title, tooltip string) *MenuItem {
	return addItem(title, tooltip, nil, false, false)
}

// AddMenuItemCheckbox 添加一个可勾选的根菜单项。
func AddMenuItemCheckbox(title, tooltip string, checked bool) *MenuItem {
	return addItem(title, tooltip, nil, true, checked)
}

// AddSeparator 在根菜单加一条分隔线。
func AddSeparator() {
	onMain(func() {
		if rootMenu == 0 {
			return
		}
		sep := sendClass(classNSMenuItem, selSeparatorItem)
		if sep != 0 {
			rootMenu.Send(selAddItem, sep)
		}
	})
}

// AddSubMenuItem 在当前项下加一个子菜单项。
func (item *MenuItem) AddSubMenuItem(title, tooltip string) *MenuItem {
	return addItem(title, tooltip, item, false, false)
}

// Click 设置点击回调。回调在独立 goroutine 里执行，避免卡住菜单栏。
func (item *MenuItem) Click(fn func()) {
	mu.Lock()
	item.click = fn
	mu.Unlock()
}

// SetTitle 修改菜单项文字。
func (item *MenuItem) SetTitle(title string) {
	mu.Lock()
	item.title = title
	ns := item.ns
	mu.Unlock()
	if ns == 0 {
		return
	}
	onMain(func() { ns.Send(selSetTitle, nsString(title)) })
}

// SetTooltip 修改菜单项提示。
func (item *MenuItem) SetTooltip(tooltip string) {
	mu.Lock()
	item.tooltip = tooltip
	ns := item.ns
	mu.Unlock()
	if ns == 0 {
		return
	}
	onMain(func() { ns.Send(selSetToolTip, nsString(tooltip)) })
}

// Disabled 报告菜单项是否置灰。
func (item *MenuItem) Disabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return item.disabled
}

// Enable 让菜单项可点。
func (item *MenuItem) Enable() { item.setEnabled(true) }

// Disable 让菜单项置灰。
func (item *MenuItem) Disable() { item.setEnabled(false) }

func (item *MenuItem) setEnabled(enabled bool) {
	mu.Lock()
	item.disabled = !enabled
	ns := item.ns
	mu.Unlock()
	if ns == 0 {
		return
	}
	onMain(func() { ns.Send(selSetEnabled, enabled) })
}

// Hide 隐藏菜单项，不从菜单里删掉（没有 Remove，槽位靠隐藏复用）。
func (item *MenuItem) Hide() { item.setHidden(true) }

// Show 重新显示隐藏的菜单项。
func (item *MenuItem) Show() { item.setHidden(false) }

func (item *MenuItem) setHidden(hidden bool) {
	mu.Lock()
	item.hidden = hidden
	ns := item.ns
	mu.Unlock()
	if ns == 0 {
		return
	}
	onMain(func() { ns.Send(selSetHidden, hidden) })
}

// Checked 报告菜单项是否打勾。
func (item *MenuItem) Checked() bool {
	mu.Lock()
	defer mu.Unlock()
	return item.checked
}

// Check 打勾。
func (item *MenuItem) Check() { item.setChecked(true) }

// Uncheck 去掉勾。
func (item *MenuItem) Uncheck() { item.setChecked(false) }

func (item *MenuItem) setChecked(checked bool) {
	mu.Lock()
	item.checked = checked
	ns := item.ns
	mu.Unlock()
	if ns == 0 {
		return
	}
	state := nsControlStateOff
	if checked {
		state = nsControlStateOn
	}
	onMain(func() { ns.Send(selSetState, state) })
}

func (item *MenuItem) String() string {
	mu.Lock()
	defer mu.Unlock()
	return fmt.Sprintf("MenuItem[%d, %q]", item.id, item.title)
}

func addItem(title, tooltip string, parent *MenuItem, checkable, checked bool) *MenuItem {
	mu.Lock()
	nextID++
	item := &MenuItem{
		id:       nextID,
		title:    title,
		tooltip:  tooltip,
		checked:  checked,
		disabled: false,
		parent:   parent,
	}
	if checkable {
		item.checked = checked
	}
	items[item.id] = item
	mu.Unlock()

	onMain(func() { item.materialize() })
	return item
}

func (item *MenuItem) materialize() {
	mu.Lock()
	title, tooltip := item.title, item.tooltip
	disabled, hidden, checked := item.disabled, item.hidden, item.checked
	parent := item.parent
	mu.Unlock()

	ns := sendClass(classNSMenuItem, selAlloc).Send(selInit)
	if ns == 0 {
		return
	}
	ns.Send(selSetTitle, nsString(title))
	if tooltip != "" {
		ns.Send(selSetToolTip, nsString(tooltip))
	}
	ns.Send(selSetTarget, target)
	ns.Send(selSetAction, selHandle)
	num := sendClass(classNSNumber, selNumberWithUnsignedLong, uint64(item.id))
	if num != 0 {
		ns.Send(selSetRepresentedObject, num)
	}
	applyItemFlags(ns, disabled, hidden, checked)

	mu.Lock()
	item.ns = ns
	// 创建过程中其它 goroutine 可能改过标题，以锁内的最新值为准再写一次。
	title, tooltip = item.title, item.tooltip
	disabled, hidden, checked = item.disabled, item.hidden, item.checked
	mu.Unlock()
	ns.Send(selSetTitle, nsString(title))
	if tooltip != "" {
		ns.Send(selSetToolTip, nsString(tooltip))
	}
	applyItemFlags(ns, disabled, hidden, checked)

	if parent == nil {
		if rootMenu != 0 {
			rootMenu.Send(selAddItem, ns)
		}
		return
	}
	parent.ensureSubmenu()
	if parent.sub != 0 {
		parent.sub.Send(selAddItem, ns)
	}
}

func applyItemFlags(ns objc.ID, disabled, hidden, checked bool) {
	ns.Send(selSetEnabled, !disabled)
	ns.Send(selSetHidden, hidden)
	state := nsControlStateOff
	if checked {
		state = nsControlStateOn
	}
	ns.Send(selSetState, state)
}

func (item *MenuItem) ensureSubmenu() {
	if item.sub != 0 {
		return
	}
	sub := sendClass(classNSMenu, selAlloc).Send(selInit)
	if sub == 0 {
		return
	}
	sub.Send(selSetAutoenablesItems, false)
	if item.ns != 0 {
		item.ns.Send(selSetSubmenu, sub)
	}
	item.sub = sub
}

func boot(onReady func()) error {
	if err := loadFrameworks(); err != nil {
		return err
	}
	if pthreadMainNP() == 0 {
		return fmt.Errorf("AppKit 必须在进程主线程初始化")
	}
	if err := registerTarget(); err != nil {
		return err
	}
	if err := createStatusItem(); err != nil {
		return err
	}
	if onReady != nil {
		onReady()
	}
	nsApp.Send(selRun)
	return nil
}

func loadFrameworks() error {
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
		return fmt.Errorf("加载 Foundation: %w", err)
	}
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
		return fmt.Errorf("加载 AppKit: %w", err)
	}
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return fmt.Errorf("加载 libSystem: %w", err)
	}
	purego.RegisterLibFunc(&dispatchAsyncF, lib, "dispatch_async_f")
	purego.RegisterLibFunc(&mainQueue, lib, "dispatch_get_main_queue")
	purego.RegisterLibFunc(&pthreadMainNP, lib, "pthread_main_np")
	purego.RegisterLibFunc(&cfRunLoopGetMain, lib, "CFRunLoopGetMain")
	purego.RegisterLibFunc(&cfRunLoopWakeUp, lib, "CFRunLoopWakeUp")
	workCallback = purego.NewCallback(func(ctx unsafe.Pointer) {
		job := (*mainJob)(ctx)
		job.fn()
		close(job.done)
	})

	selSharedApplication = objc.RegisterName("sharedApplication")
	selSetActivationPolicy = objc.RegisterName("setActivationPolicy:")
	selRun = objc.RegisterName("run")
	selStop = objc.RegisterName("stop:")
	selRetain = objc.RegisterName("retain")
	selAlloc = objc.RegisterName("alloc")
	selInit = objc.RegisterName("init")
	selStringWithUTF8String = objc.RegisterName("stringWithUTF8String:")
	selDataWithBytesLength = objc.RegisterName("dataWithBytes:length:")
	selInitWithData = objc.RegisterName("initWithData:")
	selSystemStatusBar = objc.RegisterName("systemStatusBar")
	selStatusItemWithLength = objc.RegisterName("statusItemWithLength:")
	selButton = objc.RegisterName("button")
	selSetImage = objc.RegisterName("setImage:")
	selSetTitle = objc.RegisterName("setTitle:")
	selSetToolTip = objc.RegisterName("setToolTip:")
	selSetMenu = objc.RegisterName("setMenu:")
	selAddItem = objc.RegisterName("addItem:")
	selSetAutoenablesItems = objc.RegisterName("setAutoenablesItems:")
	selSeparatorItem = objc.RegisterName("separatorItem")
	selSetTarget = objc.RegisterName("setTarget:")
	selSetAction = objc.RegisterName("setAction:")
	selSetRepresentedObject = objc.RegisterName("setRepresentedObject:")
	selRepresentedObject = objc.RegisterName("representedObject")
	selNumberWithUnsignedLong = objc.RegisterName("numberWithUnsignedLong:")
	selUnsignedLongValue = objc.RegisterName("unsignedLongValue")
	selSetEnabled = objc.RegisterName("setEnabled:")
	selSetHidden = objc.RegisterName("setHidden:")
	selSetState = objc.RegisterName("setState:")
	selSetSubmenu = objc.RegisterName("setSubmenu:")
	selHandle = objc.RegisterName("handle:")

	classNSString = objc.GetClass("NSString")
	classNSData = objc.GetClass("NSData")
	classNSImage = objc.GetClass("NSImage")
	classNSNumber = objc.GetClass("NSNumber")
	classNSMenu = objc.GetClass("NSMenu")
	classNSMenuItem = objc.GetClass("NSMenuItem")
	if classNSString == 0 || classNSData == 0 || classNSImage == 0 || classNSNumber == 0 || classNSMenu == 0 || classNSMenuItem == 0 {
		return fmt.Errorf("AppKit 类没有加载完整")
	}
	return nil
}

func registerTarget() error {
	class, err := objc.RegisterClass(
		"CFPTrayTarget",
		objc.GetClass("NSObject"),
		nil,
		nil,
		[]objc.MethodDef{{
			Cmd: selHandle,
			Fn:  menuHandle,
		}},
	)
	if err != nil {
		return err
	}
	target = objc.ID(class).Send(selAlloc).Send(selInit)
	if target == 0 {
		return fmt.Errorf("菜单点击目标创建失败")
	}
	return nil
}

func createStatusItem() error {
	appClass := objc.GetClass("NSApplication")
	if appClass == 0 {
		return fmt.Errorf("找不到 NSApplication")
	}
	nsApp = retain(objc.ID(appClass).Send(selSharedApplication))
	if nsApp == 0 {
		return fmt.Errorf("NSApplication 创建失败")
	}
	nsApp.Send(selSetActivationPolicy, nsActivationPolicyAccessory)

	barClass := objc.GetClass("NSStatusBar")
	if barClass == 0 {
		return fmt.Errorf("找不到 NSStatusBar")
	}
	bar := objc.ID(barClass).Send(selSystemStatusBar)
	if bar == 0 {
		return fmt.Errorf("系统菜单栏不可用")
	}
	statusItem = retain(bar.Send(selStatusItemWithLength, nsVariableStatusItemLength))
	if statusItem == 0 {
		return fmt.Errorf("状态栏图标创建失败")
	}
	rootMenu = sendClass(classNSMenu, selAlloc).Send(selInit)
	if rootMenu == 0 {
		return fmt.Errorf("菜单创建失败")
	}
	rootMenu.Send(selSetAutoenablesItems, false)
	statusItem.Send(selSetMenu, rootMenu)
	return nil
}

func menuHandle(_ objc.ID, _ objc.SEL, sender objc.ID) {
	if sender == 0 {
		return
	}
	rep := sender.Send(selRepresentedObject)
	if rep == 0 {
		return
	}
	id := uint32(objc.Send[uintptr](rep, selUnsignedLongValue))
	mu.Lock()
	item := items[id]
	var fn func()
	if item != nil {
		fn = item.click
	}
	mu.Unlock()
	if fn == nil {
		return
	}
	go fn()
}

func statusButton() objc.ID {
	if statusItem == 0 {
		return 0
	}
	return statusItem.Send(selButton)
}

func sendClass(class objc.Class, sel objc.SEL, args ...any) objc.ID {
	return objc.ID(class).Send(sel, args...)
}

func retain(id objc.ID) objc.ID {
	if id == 0 {
		return 0
	}
	return id.Send(selRetain)
}

func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	id := sendClass(classNSString, selStringWithUTF8String, unsafe.Pointer(&b[0]))
	runtime.KeepAlive(b)
	return id
}

type mainJob struct {
	fn   func()
	done chan struct{}
	pin  runtime.Pinner
}

func onMain(fn func()) {
	if pthreadMainNP != nil && pthreadMainNP() != 0 {
		fn()
		return
	}
	if dispatchAsyncF == nil || mainQueue == nil || workCallback == 0 {
		fn()
		return
	}
	job := &mainJob{fn: fn, done: make(chan struct{})}
	// dispatch_async_f 会把指针留到回调执行，钉住避免被 GC 挪走。
	job.pin.Pin(job)
	dispatchAsyncF(mainQueue(), unsafe.Pointer(job), workCallback)
	<-job.done
	job.pin.Unpin()
}
