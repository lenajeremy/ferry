//go:build darwin

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// enableShareMenu switches on Share › Ferry (the share extension inside Ferry.app) the first time
// the app runs: macOS adds a new app's share extension switched off. Only once, so if someone
// turns it off in System Settings it stays off.
func enableShareMenu(dataDir string) {
	done := filepath.Join(dataDir, "share-menu-on")
	if _, err := os.Stat(done); err == nil {
		return
	}
	if err := exec.Command("/usr/bin/pluginkit", "-e", "use", "-i", "app.ferry.desktop.share").Run(); err != nil {
		log.Printf("couldn't switch on Share › Ferry: %v", err)
		return
	}
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.WriteFile(done, nil, 0o600)
}
