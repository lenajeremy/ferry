package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Send with Ferry in Linux file managers, the counterpart of the Mac's Share › Ferry: right-click files
// in GNOME Files (Scripts › Send with Ferry), KDE's Dolphin or Cinnamon's Nemo and they go to Ferry,
// so they're on the phone. Each is a small file in the user's data folder (~/.local/share) that runs
// this copy of Ferry with the files; a Ferry that's already open takes them (secondInstance).
// It's plain files, so it builds everywhere and its tests run on any computer.

type fileMenu struct {
	app  string // the file manager's program, to tell whether it's installed
	path string // where its entry goes, in the data folder
	mode os.FileMode
	text func(exe string) string
}

var fileMenus = []fileMenu{
	{"nautilus", "nautilus/scripts/Send with Ferry", 0o755, nautilusScript},
	{"dolphin", "kio/servicemenus/app.ferry.send.desktop", 0o755, dolphinMenu}, // KDE only reads executable ones
	{"nemo", "nemo/actions/app.ferry.send.nemo_action", 0o644, nemoAction},
}

// addFileMenus puts Send with Ferry, pointing at exe, in each installed file manager that hasn't had it
// yet, and keeps the ones already there pointing at exe (the .tar.gz copy of Ferry can move). marker
// lists the file managers it was added to, so one that someone deletes stays deleted.
func addFileMenus(dataHome, marker, exe string, installed func(app string) bool) error {
	data, _ := os.ReadFile(marker)
	added := strings.Fields(string(data))
	var firstErr error
	for _, m := range fileMenus {
		path := filepath.Join(dataHome, m.path)
		if _, err := os.Stat(path); err != nil && (slices.Contains(added, m.app) || !installed(m.app)) {
			continue
		}
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err == nil {
			err = os.WriteFile(path, []byte(m.text(exe)), m.mode)
		}
		if err == nil {
			err = os.Chmod(path, m.mode)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !slices.Contains(added, m.app) {
			added = append(added, m.app)
		}
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(marker, []byte(strings.Join(added, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return firstErr
}

// nautilusScript: GNOME Files lists the scripts in its scripts folder under Scripts, by file name, and
// hands them the selected files (also in NAUTILUS_SCRIPT_SELECTED_FILE_PATHS, one per line, as full paths).
func nautilusScript(exe string) string {
	return `#!/bin/sh
# Added by Ferry: sends the selected files to your phone.
set -f
IFS='
'
[ -n "$NAUTILUS_SCRIPT_SELECTED_FILE_PATHS" ] && set -- $NAUTILUS_SCRIPT_SELECTED_FILE_PATHS
exec ` + shellQuote(exe) + ` "$@"
`
}

// dolphinMenu is a KDE service menu: an entry in the menu of any file (not folders), at the top level.
func dolphinMenu(exe string) string {
	return `# Added by Ferry: sends the selected files to your phone.
[Desktop Entry]
Type=Service
MimeType=all/allfiles;
X-KDE-ServiceTypes=KonqPopupMenu/Plugin
X-KDE-Priority=TopLevel
Actions=send

[Desktop Action send]
Name=Send with Ferry
Icon=app.ferry.Ferry
Exec=` + execArg(exe) + ` %F
`
}

// nemoAction is a Nemo action: an entry in the menu of any selection of files (not folders).
func nemoAction(exe string) string {
	return `# Added by Ferry: sends the selected files to your phone.
[Nemo Action]
Name=Send with Ferry
Comment=Send the selected files to your phone
Exec=` + execArg(exe) + ` %F
Icon-Name=app.ferry.Ferry
Selection=notnone
Extensions=nodirs;
Quote=double
`
}

// execArg is a path as one argument of an Exec line (desktop entries and Nemo actions read them
// alike): in double quotes if it needs them, then with backslashes escaped for the file itself.
func execArg(s string) string {
	plain := strings.IndexFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("/._-+", r)
	}) < 0
	if !plain {
		s = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(s) + `"`
	}
	return strings.ReplaceAll(s, `\`, `\\`)
}

func shellQuote(s string) string { return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'` }
