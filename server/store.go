package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Entry is one file in the shared folder, as the page shows it.
type Entry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Added int64  `json:"added"` // unix ms: when it arrived in the folder
	Kind  string `json:"kind"`  // image, video, audio, pdf or file
	From  string `json:"from"`  // the device that sent it ("iPhone", "Mac", …)
	// Native: a browser shows it as is; other images (HEIC, TIFF, RAW) are shown from a preview.
	Native bool `json:"native,omitempty"`
	// Version changes whenever the file does, so cached thumbnails are refreshed.
	Version string `json:"v"`
	modTime time.Time
}

type meta struct {
	From  string `json:"from"`
	Added int64  `json:"added"`
}

// Store is the shared folder: every file directly inside it (not hidden, not in subfolders) is
// shared. Who sent each file and when it arrived are kept beside the app's other data.
type Store struct {
	dir      string
	metaPath string

	mu     sync.Mutex
	meta   map[string]meta
	primed bool // the folder has been listed once since start-up

	// useTrash: deleting moves files to the system's bin (off in tests).
	useTrash bool
}

const partPrefix = ".ferry-upload-"

func OpenStore(dir, appDir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, metaPath: filepath.Join(appDir, "files.json"), meta: map[string]meta{}}
	s.useTrash = true
	if b, err := os.ReadFile(s.metaPath); err == nil {
		_ = json.Unmarshal(b, &s.meta)
	}
	// Uploads cut off by a quit leave a hidden part file behind.
	if parts, _ := filepath.Glob(filepath.Join(dir, partPrefix+"*")); len(parts) > 0 {
		for _, p := range parts {
			os.Remove(p)
		}
	}
	return s, nil
}

// validName accepts only a plain file name inside the folder: no paths, nothing hidden.
func validName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.HasPrefix(name, ".") &&
		!strings.ContainsAny(name, "/\\\x00") && utf8.ValidString(name) && len(name) <= 255
}

// Path is where a shared file lives; an error when the name isn't a shared file.
func (s *Store) Path(name string) (string, error) {
	if !validName(name) {
		return "", os.ErrNotExist
	}
	p := filepath.Join(s.dir, name)
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return p, nil
}

// List returns the shared files, newest first. Files it hasn't seen before are recorded as
// arriving now, from this Mac (dropped into the folder in Finder).
func (s *Store) List() ([]Entry, error) {
	items, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	changed := false
	seen := map[string]bool{}
	var out []Entry
	for _, it := range items {
		name := it.Name()
		if !validName(name) || !it.Type().IsRegular() {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		seen[name] = true
		m, ok := s.meta[name]
		if !ok {
			// New to Ferry. Seen while it's running, it has just been put in the folder; at
			// start-up (first launch, or added while Ferry was off) its own date is the best guess.
			m = meta{From: "Mac", Added: now}
			if !s.primed {
				m.Added = info.ModTime().UnixMilli()
			}
			s.meta[name] = m
			changed = true
		}
		kind, native := kindOf(name)
		out = append(out, Entry{
			Name: name, Size: info.Size(), Added: m.Added, Kind: kind, From: m.From, Native: native,
			Version: fmt.Sprintf("%x-%x", info.ModTime().UnixNano(), info.Size()), modTime: info.ModTime(),
		})
	}
	s.primed = true
	for name := range s.meta {
		if !seen[name] {
			delete(s.meta, name)
			changed = true
		}
	}
	if changed {
		s.saveMeta()
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if a.Added != b.Added {
			if a.Added > b.Added {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func (s *Store) saveMeta() {
	b, _ := json.MarshalIndent(s.meta, "", "  ")
	tmp := s.metaPath + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, s.metaPath)
	}
}

// Save writes an upload into the folder under a free name (photo.jpg, photo 2.jpg, …). The file
// only appears once it's complete.
func (s *Store) Save(name string, body io.Reader, from string) (Entry, error) {
	name = cleanName(name)
	var rnd [8]byte
	rand.Read(rnd[:])
	part := filepath.Join(s.dir, partPrefix+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Entry{}, err
	}
	_, err = io.CopyBuffer(f, body, make([]byte, 1<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return Entry{}, err
	}

	s.mu.Lock()
	final := s.freeName(name)
	err = os.Rename(part, filepath.Join(s.dir, final))
	if err == nil {
		s.meta[final] = meta{From: from, Added: time.Now().UnixMilli()}
		s.saveMeta()
	}
	s.mu.Unlock()
	if err != nil {
		os.Remove(part)
		return Entry{}, err
	}
	entries, _ := s.List()
	for _, e := range entries {
		if e.Name == final {
			return e, nil
		}
	}
	return Entry{}, errors.New("saved file disappeared")
}

// freeName finds a name nothing in the folder has: "a.jpg", then "a 2.jpg", "a 3.jpg", …
func (s *Store) freeName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s %d%s", base, i, ext)
		}
		if _, err := os.Lstat(filepath.Join(s.dir, candidate)); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

// cleanName makes a sent name safe to save. iPhones name every picked photo "image.jpg" (and
// videos "video.mov"), so those get the time they arrived instead, as macOS names screenshots.
func cleanName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', 0:
			return '-'
		}
		if r < 32 {
			return -1
		}
		return r
	}, filepath.Base(name)))
	name = strings.TrimLeft(name, ".")
	if name == "" {
		name = "File"
	}
	ext := filepath.Ext(name)
	switch strings.ToLower(strings.TrimSuffix(name, ext)) {
	case "image", "video":
		label := "Photo"
		if k, _ := kindOf(name); k == "video" {
			label = "Video"
		}
		name = label + " " + time.Now().Format("2006-01-02 at 15.04.05") + strings.ToLower(ext)
	}
	for len(name) > 200 {
		r := []rune(strings.TrimSuffix(name, ext))
		name = string(r[:len(r)-1]) + ext
	}
	return name
}

// Delete moves a shared file to the Trash (the Recycle Bin on Windows), so a wrong tap on the phone
// can be undone on the computer. Without one, the file is removed.
func (s *Store) Delete(name string) error {
	p, err := s.Path(name)
	if err != nil {
		return err
	}
	err = errNoTrash
	if s.useTrash {
		err = moveToTrash(p)
		if err != nil && !errors.Is(err, errNoTrash) {
			return fmt.Errorf("moving to the %s: %w", trashName(), err)
		}
	}
	if errors.Is(err, errNoTrash) {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.meta, name)
	s.saveMeta()
	s.mu.Unlock()
	return nil
}

// Signature changes when anything in the folder does (names, sizes, times).
func Signature(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s|%s|%d|%s\n", e.Name, e.Version, e.Added, e.From)
	}
	return b.String()
}

var nativeImages = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".bmp": true, ".svg": true}
var otherImages = map[string]bool{".heic": true, ".heif": true, ".tif": true, ".tiff": true, ".dng": true, ".cr2": true, ".cr3": true, ".nef": true, ".arw": true, ".raf": true, ".orf": true, ".rw2": true}
var videos = map[string]bool{".mp4": true, ".mov": true, ".m4v": true, ".webm": true, ".3gp": true}
var audio = map[string]bool{".mp3": true, ".m4a": true, ".wav": true, ".aac": true, ".aiff": true, ".flac": true, ".ogg": true}

func kindOf(name string) (kind string, native bool) {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case nativeImages[ext]:
		return "image", true
	case otherImages[ext]:
		return "image", false
	case videos[ext]:
		return "video", false
	case audio[ext]:
		return "audio", false
	case ext == ".pdf":
		return "pdf", false
	}
	return "file", false
}
