package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	comdlg32            = windows.NewLazySystemDLL("comdlg32.dll")
	procGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
	procDialogError     = comdlg32.NewProc("CommDlgExtendedError")
)

type openFileName struct {
	size                   uint32
	owner, instance        uintptr
	filter                 *uint16
	customFilter           *uint16
	maxCustom, filterIndex uint32
	file                   *uint16
	maxFile                uint32
	fileTitle              *uint16
	maxFileTitle           uint32
	initialDir, title      *uint16
	flags                  uint32
	fileOffset, extension  uint16
	defaultExtension       *uint16
	customData, hook       uintptr
	template               *uint16
	reserved               uintptr
	reservedFlags, flagsEx uint32
}

func chooseWallpaperFile(hwnd uintptr, current string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const ofnFlags = 0x00000008 | 0x00000800 | 0x00001000 | 0x00080000 // NOCHANGEDIR, PATHMUSTEXIST, FILEMUSTEXIST, EXPLORER
	filter := utf16.Encode([]rune("图片文件\x00*.png;*.jpg;*.jpeg;*.gif\x00\x00"))
	title, _ := windows.UTF16PtrFromString("选择壁纸")
	var directory *uint16
	if current != "" {
		directory, _ = windows.UTF16PtrFromString(filepath.Dir(current))
	}
	path := make([]uint16, 32768)
	options := openFileName{
		owner: hwnd, filter: &filter[0], filterIndex: 1, file: &path[0], maxFile: uint32(len(path)),
		initialDir: directory, title: title, flags: ofnFlags,
	}
	options.size = uint32(unsafe.Sizeof(options))
	ok, _, _ := procGetOpenFileName.Call(uintptr(unsafe.Pointer(&options)))
	if ok == 0 {
		return "", nativeDialogError("打开壁纸选择器")
	}
	return windows.UTF16ToString(path), nil
}

func showFontDirectory(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, file, nil, nil, windows.SW_SHOWNORMAL)
}

func nativeDialogError(operation string) error {
	code, _, _ := procDialogError.Call()
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%s失败（0x%X）", operation, code)
}
