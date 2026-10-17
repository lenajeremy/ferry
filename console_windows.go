package main

import (
	"syscall"
	"unsafe"
)

// enableTerminalColours turns on ANSI escape codes in the Windows console, for the QR code.
func enableTerminalColours() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getMode, setMode := kernel32.NewProc("GetConsoleMode"), kernel32.NewProc("SetConsoleMode")
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if r, _, _ := getMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return
	}
	const enableVirtualTerminalProcessing = 0x0004
	setMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
}
