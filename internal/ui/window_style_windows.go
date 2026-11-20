package ui

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/unit"
	"golang.org/x/sys/windows"

	"gon/internal/config"
)

var user32 = windows.NewLazySystemDLL("user32.dll")

var (
	getForegroundWindow = user32.NewProc("GetForegroundWindow")
	isIconic            = user32.NewProc("IsIconic")
	getWindowDPI        = user32.NewProc("GetDpiForWindow")
	getWindowDPIContext = user32.NewProc("GetWindowDpiAwarenessContext")
	setThreadDPIContext = user32.NewProc("SetThreadDpiAwarenessContext")
	getCursorPos        = user32.NewProc("GetCursorPos")
	screenToClient      = user32.NewProc("ScreenToClient")
	getAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	getSystemMetrics    = user32.NewProc("GetSystemMetrics")
	getMonitorDPI       = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
	monitorFromWindow   = user32.NewProc("MonitorFromWindow")
	monitorFromRect     = user32.NewProc("MonitorFromRect")
	getMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	setWindowPos        = user32.NewProc("SetWindowPos")
	procSetWindowText   = user32.NewProc("SetWindowTextW")
	getWindowLong       = user32.NewProc("GetWindowLongW")
	setWindowLong       = user32.NewProc("SetWindowLongW")
	getClassLong        = user32.NewProc("GetClassLongW")
	setClassLong        = user32.NewProc("SetClassLongW")
	setLayeredAlpha     = user32.NewProc("SetLayeredWindowAttributes")
)

func setWindowTitle(hwnd uintptr, title string) {
	if text, err := windows.UTF16PtrFromString(title); err == nil {
		procSetWindowText.Call(hwnd, uintptr(unsafe.Pointer(text)))
	}
}

func windowDragCursor(hwnd uintptr) (image.Point, bool) {
	if hwnd == 0 {
		return image.Point{}, false
	}
	const (
		smSwapButton  = 23
		vkLeftButton  = 0x01
		vkRightButton = 0x02
		keyDown       = 0x8000
	)
	button := uintptr(vkLeftButton)
	if swapped, _, _ := getSystemMetrics.Call(smSwapButton); swapped != 0 {
		button = vkRightButton
	}
	if state, _, _ := getAsyncKeyState.Call(button); state&keyDown == 0 {
		return image.Point{}, false
	}

	// Layout runs on a Go goroutine. Match the HWND's DPI context so the
	// converted point is in the same client pixels as Gio's pointer events.
	dpiContext, _, _ := getWindowDPIContext.Call(hwnd)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, _, _ := setThreadDPIContext.Call(dpiContext)
	if previous == 0 {
		return image.Point{}, false
	}
	defer setThreadDPIContext.Call(previous)
	var pt struct{ X, Y int32 }
	if ok, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&pt))); ok == 0 {
		return image.Point{}, false
	}
	if ok, _, _ := screenToClient.Call(hwnd, uintptr(unsafe.Pointer(&pt))); ok == 0 {
		return image.Point{}, false
	}
	return image.Pt(int(pt.X), int(pt.Y)), true
}

// StartupWindowSize clamps the initial window before Gio shows it.
func StartupWindowSize(window config.Window) app.Option {
	return windowSizeOption(window, 0)
}

func windowSizeOption(window config.Window, hwnd uintptr) app.Option {
	return func(metric unit.Metric, cfg *app.Config) {
		target := hwnd
		if target == 0 {
			target, _, _ = getForegroundWindow.Call()
		}
		monitor := placementMonitor(target, window.Position)
		cfg.Size, cfg.MinSize = windowSize(window, monitorWorkArea(monitor), monitorMetric(monitor, metric))
		cfg.Mode = app.Windowed
	}
}

func windowSize(window config.Window, work image.Rectangle, metric unit.Metric) (size, minimum image.Point) {
	minimum = image.Pt(metric.Dp(config.MinWindowWidth), metric.Dp(config.MinWindowHeight))
	size = image.Pt(
		metric.Dp(unit.Dp(min(window.Width, config.MaxWindowSize))),
		metric.Dp(unit.Dp(min(window.Height, config.MaxWindowSize))),
	)
	size = image.Pt(max(minimum.X, size.X), max(minimum.Y, size.Y))
	if !work.Empty() {
		minimum.X, minimum.Y = min(minimum.X, work.Dx()), min(minimum.Y, work.Dy())
		size.X, size.Y = min(size.X, work.Dx()), min(size.Y, work.Dy())
	}
	return size, minimum
}

func placementMonitor(hwnd uintptr, position config.WindowPosition) uintptr {
	const monitorDefaultToNearest = 2
	const monitorDefaultToPrimary = 1
	if !position.Centered {
		x, y := min(position.X, math.MaxInt32-1), min(position.Y, math.MaxInt32-1)
		point := windows.Rect{Left: x, Top: y, Right: x + 1, Bottom: y + 1}
		monitor, _, _ := monitorFromRect.Call(uintptr(unsafe.Pointer(&point)), monitorDefaultToNearest)
		return monitor
	}
	mode := monitorDefaultToNearest
	if hwnd == 0 {
		mode = monitorDefaultToPrimary
	}
	monitor, _, _ := monitorFromWindow.Call(hwnd, uintptr(mode))
	return monitor
}

func monitorMetric(monitor uintptr, fallback unit.Metric) unit.Metric {
	var dpiX, dpiY uint32
	const effectiveDPI = 0
	if result, _, _ := getMonitorDPI.Call(monitor, effectiveDPI, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); result == 0 && dpiX > 0 {
		fallback.PxPerDp = float32(dpiX) / 96
	}
	return fallback
}

func monitorWorkArea(monitor uintptr) image.Rectangle {
	info := struct {
		size         uint32
		bounds, work windows.Rect
		flags        uint32
	}{}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return image.Rectangle{}
	}
	return image.Rect(int(info.work.Left), int(info.work.Top), int(info.work.Right), int(info.work.Bottom))
}

func placeConfiguredWindow(hwnd uintptr, window config.Window) {
	monitor := placementMonitor(hwnd, window.Position)
	work := monitorWorkArea(monitor)
	if work.Empty() {
		return
	}
	dpi, _, _ := getWindowDPI.Call(hwnd)
	metric := monitorMetric(monitor, unit.Metric{PxPerDp: float32(dpi) / 96})
	size, _ := windowSize(window, work, metric)
	desired := positionedWindowRect(size, work, window.Position)
	// Gio may apply the suggested WM_DPICHANGED rectangle during the first move.
	for range 2 {
		var bounds windows.Rect
		if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
			return
		}
		current := image.Rect(int(bounds.Left), int(bounds.Top), int(bounds.Right), int(bounds.Bottom))
		if current == desired {
			return
		}
		const noZOrderOrActivation = 0x0004 | 0x0010
		setWindowPos.Call(hwnd, 0, uintptr(desired.Min.X), uintptr(desired.Min.Y), uintptr(desired.Dx()), uintptr(desired.Dy()), noZOrderOrActivation)
	}
}

func positionedWindowRect(size image.Point, work image.Rectangle, position config.WindowPosition) image.Rectangle {
	origin := image.Pt(
		min(work.Max.X, max(work.Min.X, int(position.X))),
		min(work.Max.Y, max(work.Min.Y, int(position.Y))),
	)
	if position.Centered {
		origin = work.Min.Add(work.Size().Sub(size).Div(2))
	}
	return fitWindowRect(image.Rectangle{Min: origin, Max: origin.Add(size)}, work)
}

func (a *App) applyWindowGeometry() {
	hwnd := a.hwnd.Load()
	if hwnd == 0 {
		return
	}
	window := a.cfg.Window
	a.win.Option(windowSizeOption(window, hwnd))
	a.win.Run(func() { placeConfiguredWindow(hwnd, window) })
	if window.Fullscreen {
		a.perform(system.ActionFullscreen)
	}
}

func fitWindowRect(bounds, work image.Rectangle) image.Rectangle {
	if bounds.Empty() || work.Empty() {
		return bounds
	}
	size := image.Pt(min(bounds.Dx(), work.Dx()), min(bounds.Dy(), work.Dy()))
	origin := image.Pt(
		max(work.Min.X, min(bounds.Min.X, work.Max.X-size.X)),
		max(work.Min.Y, min(bounds.Min.Y, work.Max.Y-size.Y)),
	)
	return image.Rectangle{Min: origin, Max: origin.Add(size)}
}

func isWindowMinimized(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	minimized, _, _ := isIconic.Call(hwnd)
	return minimized != 0
}

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaWindowCornerPref     = 33
	dwmwaBorderColor          = 34
	dwmwcpRound               = 2
	dwmColorNone              = 0xFFFFFFFE
)

// Keep the native window trim. Wallpaper and blur are rendered by the app.
func styleWindow(hwnd uintptr) {
	dark := int32(1)
	round := int32(dwmwcpRound)
	border := uint32(dwmColorNone)
	_ = windows.DwmSetWindowAttribute(windows.HWND(hwnd), dwmwaUseImmersiveDarkMode, unsafe.Pointer(&dark), 4)
	_ = windows.DwmSetWindowAttribute(windows.HWND(hwnd), dwmwaWindowCornerPref, unsafe.Pointer(&round), 4)
	_ = windows.DwmSetWindowAttribute(windows.HWND(hwnd), dwmwaBorderColor, unsafe.Pointer(&border), 4)
}

func (a *App) updateWindowOpacity() {
	hwnd := a.hwnd.Load()
	if hwnd == 0 || a.win == nil {
		return
	}
	transparency := a.cfg.Window.Transparency
	if a.settings.open {
		transparency = a.settings.draft.Window.Transparency
	}
	alpha := uint8(255*(1-clampF(transparency, 0, config.MaxWindowTransparency)) + 0.5)
	if a.windowAlpha == alpha {
		return
	}
	a.windowAlpha = alpha
	var err error
	a.win.Run(func() { err = setWindowOpacity(hwnd, alpha) })
	if err != nil {
		a.notify("无法调整窗口透明度（" + err.Error() + "）")
	}
}

// Windows applies the alpha to the existing Gio frame as a single layer.
func setWindowOpacity(hwnd uintptr, alpha uint8) error {
	const (
		wsExLayered = 0x00080000
		csOwnDC     = 0x0020
	)
	exStyle, classStyle := int32(-20), int32(-26) // GWL_EXSTYLE, GCL_STYLE
	style, _, _ := getWindowLong.Call(hwnd, uintptr(exStyle))
	if style&wsExLayered != 0 {
		return setLayeredWindowAlpha(hwnd, alpha)
	}
	if alpha == 0xff {
		return nil
	}
	// Layered windows require a class without CS_OWNDC. Gio still owns its
	// original handle and renders through the same swap chain.
	class, _, _ := getClassLong.Call(hwnd, uintptr(classStyle))
	if class&csOwnDC != 0 {
		if old, _, err := setClassLong.Call(hwnd, uintptr(classStyle), class&^csOwnDC); old == 0 {
			return fmt.Errorf("SetClassLongW: %w", err)
		}
	}
	_, _, styleErr := setWindowLong.Call(hwnd, uintptr(exStyle), style|wsExLayered)
	actual, _, _ := getWindowLong.Call(hwnd, uintptr(exStyle))
	if actual&wsExLayered == 0 {
		if class&csOwnDC != 0 {
			setClassLong.Call(hwnd, uintptr(classStyle), class)
		}
		return fmt.Errorf("SetWindowLongW: %w", styleErr)
	}
	if err := setLayeredWindowAlpha(hwnd, alpha); err != nil {
		setWindowLong.Call(hwnd, uintptr(exStyle), style)
		if class&csOwnDC != 0 {
			setClassLong.Call(hwnd, uintptr(classStyle), class)
		}
		return err
	}
	return nil
}

func setLayeredWindowAlpha(hwnd uintptr, alpha uint8) error {
	const lwaAlpha = 0x02
	if ok, _, err := setLayeredAlpha.Call(hwnd, 0, uintptr(alpha), lwaAlpha); ok == 0 {
		return fmt.Errorf("SetLayeredWindowAttributes: %w", err)
	}
	return nil
}
