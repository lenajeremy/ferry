package server

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// What differs between macOS, Windows and Linux: opening things, the trash, names, where data lives.

// Open opens a web address, file or folder in its usual app.
func Open(target string) error { return openThing(target) }

// openThing opens a web address, file or folder in its usual app.
func openThing(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

// revealThing shows a file in the file manager, selected where it can be.
func revealThing(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}

// localDevice is what files added on this computer are labelled as coming from.
func localDevice() string {
	switch runtime.GOOS {
	case "darwin":
		return "Mac"
	case "windows":
		return "PC"
	}
	return "Computer"
}

// fileManager is what the page calls the place files are shown.
func fileManager() string {
	switch runtime.GOOS {
	case "darwin":
		return "Finder"
	case "windows":
		return "File Explorer"
	}
	return "Files"
}

// trashName is what the page calls the bin deleted files go to.
func trashName() string {
	if runtime.GOOS == "windows" {
		return "Recycle Bin"
	}
	return "Trash"
}

var errNoTrash = errors.New("no trash on this system")

// moveToTrash moves a file to the system's bin, so a deletion can be undone.
func moveToTrash(path string) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("trash"); err != nil { // macOS 14 and later
			return errNoTrash
		}
		return runCmd(exec.Command("trash", path))
	case "windows":
		quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
		return runCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile("+quoted+", 'OnlyErrorDialogs', 'SendToRecycleBin')"))
	default:
		if _, err := exec.LookPath("gio"); err == nil {
			return runCmd(exec.Command("gio", "trash", path))
		}
		return freedesktopTrash(path)
	}
}

func runCmd(cmd *exec.Cmd) error {
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// freedesktopTrash follows the Trash specification by hand (desktops without gio): the file goes to
// ~/.local/share/Trash/files with a .trashinfo saying where it came from.
func freedesktopTrash(path string) error {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	files, info := filepath.Join(base, "Trash", "files"), filepath.Join(base, "Trash", "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return err
	}
	name := filepath.Base(path)
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(files, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		ext := filepath.Ext(filepath.Base(path))
		name = fmt.Sprintf("%s.%d%s", strings.TrimSuffix(filepath.Base(path), ext), i, ext)
	}
	abs, _ := filepath.Abs(path)
	record := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", (&url.URL{Path: abs}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(filepath.Join(info, name+".trashinfo"), []byte(record), 0o600); err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(files, name)); err != nil {
		os.Remove(filepath.Join(info, name+".trashinfo"))
		return err
	}
	return nil
}

// AppDataDir is where Ferry keeps its code, certificate and thumbnails:
// ~/Library/Application Support/Ferry, %AppData%\Ferry, or ~/.config/Ferry.
func AppDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "Ferry")
}

// nameOverride is -name: what the phones call this computer.
var nameOverride string

// computerNames: the name people know the computer by, and its Bonjour (.local) host name.
func computerNames() (name, local string) {
	defer func() {
		if nameOverride != "" {
			name = nameOverride
		}
		// FERRY_DEMO=1 (screenshots): a generic computer, not this one.
		if os.Getenv("FERRY_DEMO") == "1" {
			local = "macbook-pro"
		}
	}()
	if runtime.GOOS == "darwin" {
		name, local = scutil("ComputerName"), scutil("LocalHostName")
	}
	host, _ := os.Hostname()
	host = strings.TrimSuffix(host, ".local")
	if local == "" {
		local = host
	}
	if name == "" {
		name = host
	}
	if name == "" {
		name = "Computer"
	}
	return name, local
}

func scutil(key string) string {
	out, err := exec.Command("scutil", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
