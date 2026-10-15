package backend

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32                        = windows.NewLazySystemDLL("shell32.dll")
	procILCreateFromPathW          = shell32.NewProc("ILCreateFromPathW")
	procILFree                     = shell32.NewProc("ILFree")
	procSHOpenFolderAndSelectItems = shell32.NewProc("SHOpenFolderAndSelectItems")
)

// platformOpen calls the shell API directly rather than running
// explorer.exe: ShellExecute opens a folder, SHOpenFolderAndSelectItems
// selects an item in its parent folder.
func platformOpen(abs string, reveal bool) error {
	path, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return err
	}
	return withCOM(func() error {
		if !reveal {
			return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), path, nil, nil, windows.SW_SHOWNORMAL)
		}
		// x/sys has no wrapper for the PIDL functions; passing the UTF-16
		// path to the raw proc needs the uintptr(unsafe.Pointer) form, which
		// LazyProc.Call (go:uintptrescapes) keeps alive for the call.
		pidl, _, _ := procILCreateFromPathW.Call(uintptr(unsafe.Pointer(path)))
		if pidl == 0 {
			return errors.New("ILCreateFromPathW failed")
		}
		defer procILFree.Call(pidl)
		if hr, _, _ := procSHOpenFolderAndSelectItems.Call(pidl, 0, 0, 0); hr != 0 {
			return fmt.Errorf("SHOpenFolderAndSelectItems: %w", syscall.Errno(hr))
		}
		return nil
	})
}

// withCOM runs fn on one OS thread with COM initialized, as the shell
// functions require.
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err {
	case nil, syscall.Errno(windows.S_FALSE):
		defer windows.CoUninitialize()
	case syscall.Errno(windows.RPC_E_CHANGED_MODE):
		// Already initialized multithreaded on this thread; usable as is,
		// and not ours to uninitialize.
	default:
		return fmt.Errorf("initialize COM: %w", err)
	}
	return fn()
}
