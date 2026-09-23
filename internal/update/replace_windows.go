//go:build windows

package update

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replaceExecutable swaps dst for src. Windows cannot delete or overwrite a
// running executable, but it CAN rename one, so ReplaceFileW moves the
// current binary aside to "<dst>.old" and puts the new file in place. The
// backup is removed by the next invocation (CleanupPendingUpdate).
func replaceExecutable(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return fmt.Errorf("encode staged path: %w", err)
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return fmt.Errorf("encode target path: %w", err)
	}
	if _, statErr := os.Lstat(dst); os.IsNotExist(statErr) {
		// No existing binary: a plain move is enough.
		return windows.MoveFileEx(from, to, 0)
	} else if statErr != nil {
		return statErr
	}
	backup := dst + ".old"
	if _, statErr := os.Lstat(backup); statErr == nil {
		return fmt.Errorf("a previous update left %q behind; remove it and re-run (the running binary cannot be replaced while the backup exists)", backup)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	backupPtr, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return fmt.Errorf("encode backup path: %w", err)
	}
	if err := replaceFileW.Find(); err != nil {
		return err
	}
	ok, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(to)),
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(backupPtr)),
		0, 0, 0,
	)
	if ok == 0 {
		return fmt.Errorf("ReplaceFileW: %w", callErr)
	}
	return nil
}

// CleanupPendingUpdate removes the backup ReplaceFileW left behind. It runs
// on every command start: by then the previous process has exited, so the
// file is no longer locked. Failures are deliberately swallowed — a stale
// backup must never fail an unrelated command — except that a backup which
// still cannot be removed is reported by replaceExecutable on the next
// update attempt.
func CleanupPendingUpdate() error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if link, err := os.Readlink(exe); err == nil {
		exe = link
	}
	_ = os.Remove(exe + ".old")
	return nil
}
