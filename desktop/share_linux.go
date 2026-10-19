//go:build linux

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// enableShareMenu adds Send with Ferry to the file managers on this computer (see filemenus.go).
func enableShareMenu(dataDir string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	installed := func(app string) bool { _, err := exec.LookPath(app); return err == nil }
	if err := addFileMenus(dataHome, filepath.Join(dataDir, "share-menu-on"), exe, installed); err != nil {
		log.Printf("couldn't add Send with Ferry to the file managers: %v", err)
	}
}
