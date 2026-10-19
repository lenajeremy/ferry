package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestFilesIn(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a photo.jpg", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	os.Mkdir(filepath.Join(dir, "Holiday"), 0o755)
	got := filesIn([]string{filepath.Join(dir, "a photo.jpg"), "notes.txt", "Holiday", "-psn_0_1234", "missing.txt", ""}, dir)
	want := []string{filepath.Join(dir, "a photo.jpg"), filepath.Join(dir, "notes.txt"), filepath.Join(dir, "Holiday")}
	if !slices.Equal(got, want) {
		t.Fatalf("filesIn = %q, want %q", got, want)
	}
}

func TestAddFileMenus(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	marker := filepath.Join(data, "share-menu-on")
	have := map[string]bool{"nautilus": true, "dolphin": true}
	installed := func(app string) bool { return have[app] }
	entry := func(app string) string {
		for _, m := range fileMenus {
			if m.app == app {
				return filepath.Join(home, m.path)
			}
		}
		t.Fatalf("no menu for %s", app)
		return ""
	}
	read := func(app string) string {
		b, err := os.ReadFile(entry(app))
		if err != nil {
			return ""
		}
		return string(b)
	}

	// First launch: only the installed file managers get it, the KDE one executable.
	if err := addFileMenus(home, marker, "/usr/lib/ferry/Ferry", installed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read("nautilus"), "exec '/usr/lib/ferry/Ferry' \"$@\"") ||
		!strings.Contains(read("dolphin"), "Exec=/usr/lib/ferry/Ferry %F") || read("nemo") != "" {
		t.Fatalf("first launch wrote:\n%s\n%s\nnemo: %q", read("nautilus"), read("dolphin"), read("nemo"))
	}
	if info, _ := os.Stat(entry("dolphin")); info.Mode().Perm() != 0o755 {
		t.Errorf("Dolphin's entry is %v, want 0755 (KDE ignores it otherwise)", info.Mode().Perm())
	}

	// Ferry moved: the entries follow it.
	moved := "/home/me/My Apps/Ferry/Ferry"
	if err := addFileMenus(home, marker, moved, installed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read("dolphin"), `Exec="/home/me/My Apps/Ferry/Ferry" %F`) {
		t.Errorf("Dolphin's entry didn't follow Ferry:\n%s", read("dolphin"))
	}

	// Deleted by the user: stays deleted. Nemo installed later: it gets one.
	os.Remove(entry("nautilus"))
	have["nemo"] = true
	if err := addFileMenus(home, marker, moved, installed); err != nil {
		t.Fatal(err)
	}
	if read("nautilus") != "" {
		t.Error("the deleted GNOME Files script came back")
	}
	if !strings.Contains(read("nemo"), `Exec="/home/me/My Apps/Ferry/Ferry" %F`) {
		t.Errorf("Nemo didn't get its action:\n%s", read("nemo"))
	}
}

func TestExecArg(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/lib/ferry/Ferry":    "/usr/lib/ferry/Ferry",
		"/opt/Ferry Apps/Ferry":   `"/opt/Ferry Apps/Ferry"`,
		`/home/a"b/$x/Ferry`:      `"/home/a\\"b/\\$x/Ferry"`,
		`/home/back\slash/Ferry`:  `"/home/back\\\\slash/Ferry"`,
		"/home/zoë/Téléchargés/F": "/home/zoë/Téléchargés/F",
	} {
		if got := execArg(in); got != want {
			t.Errorf("execArg(%q) = %s, want %s", in, got, want)
		}
	}
}

// The GNOME Files script hands Ferry the selected files, spaces and all.
func TestNautilusScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "it's Ferry")
	os.WriteFile(fake, []byte("#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done > '"+out+"'\n"), 0o755)
	script := filepath.Join(dir, "Send with Ferry")
	os.WriteFile(script, []byte(nautilusScript(fake)), 0o755)

	run := func(env string, args ...string) string {
		cmd := exec.Command(script, args...)
		cmd.Env = append(os.Environ(), env)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		b, _ := os.ReadFile(out)
		return string(b)
	}
	if got := run("NAUTILUS_SCRIPT_SELECTED_FILE_PATHS=/p/a photo.jpg\n/p/*.txt\n", "a photo.jpg", "*.txt"); got != "[/p/a photo.jpg]\n[/p/*.txt]\n" {
		t.Errorf("with the paths from GNOME Files, Ferry got:\n%s", got)
	}
	if got := run("NAUTILUS_SCRIPT_SELECTED_FILE_PATHS=", "/p/one file", "/p/two"); got != "[/p/one file]\n[/p/two]\n" {
		t.Errorf("with arguments only, Ferry got:\n%s", got)
	}
}
