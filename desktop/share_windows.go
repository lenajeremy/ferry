//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// enableShareMenu adds Ferry to File Explorer's Send to menu (a shortcut in the user's SendTo folder),
// the counterpart of the Mac's Share › Ferry: select files, Send to › Ferry, and they're on the phone.
// Added the first time only, so if someone deletes it, it stays gone; after that it's kept pointing at
// this copy of Ferry, since Ferry.exe runs from wherever it was downloaded to.
func enableShareMenu(dataDir string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	appData, err := os.UserConfigDir()
	if err != nil {
		return
	}
	link := filepath.Join(appData, "Microsoft", "Windows", "SendTo", "Ferry.lnk")
	done := filepath.Join(dataDir, "share-menu-on")
	if _, err := os.Stat(done); err == nil {
		if _, err := os.Stat(link); err != nil {
			return
		}
	}
	if err := shortcut(link, exe); err != nil {
		log.Printf("couldn't add Ferry to Send to: %v", err)
		return
	}
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.WriteFile(done, nil, 0o600)
}

// shortcut saves a shortcut (.lnk) at link that opens target, with target's icon.
func shortcut(link, target string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		e, ok := err.(*ole.OleError)
		switch {
		case ok && e.Code() == 1: // S_FALSE: COM was already set up on this thread; still needs undoing
			defer ole.CoUninitialize()
		case ok && e.Code() == 0x80010106: // RPC_E_CHANGED_MODE: set up another way, which works too
		default:
			return err
		}
	} else {
		defer ole.CoUninitialize()
	}

	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer unknown.Release()
	shell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()
	v, err := oleutil.CallMethod(shell, "CreateShortcut", link)
	if err != nil {
		return err
	}
	lnk := v.ToIDispatch()
	defer lnk.Release()
	for _, p := range [][2]string{
		{"TargetPath", target},
		{"WorkingDirectory", filepath.Dir(target)},
		{"IconLocation", target + ",0"},
		{"Description", "Send to your phone with Ferry"},
	} {
		if _, err := oleutil.PutProperty(lnk, p[0], p[1]); err != nil {
			return err
		}
	}
	_, err = oleutil.CallMethod(lnk, "Save")
	return err
}
