package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Thumbs makes and caches smaller pictures of shared files. On a Mac with its own tools: sips for
// photos (HEIC and RAW included) and Quick Look for videos, PDFs and documents. Elsewhere with Go
// and ffmpeg (see thumbs_portable.go).
type Thumbs struct {
	dir string
	sem chan struct{}

	mu       sync.Mutex
	inflight map[string]*thumbCall
	failed   map[string]bool
}

type thumbCall struct {
	done chan struct{}
	path string
	err  error
}

var errNoThumb = errors.New("no thumbnail for this file")

func NewThumbs(dir string) (*Thumbs, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Thumbs{dir: dir, sem: make(chan struct{}, 3), inflight: map[string]*thumbCall{}, failed: map[string]bool{}}, nil
}

// Get returns a JPEG (or PNG, from Quick Look) of src no bigger than size on its long side.
func (t *Thumbs) Get(src string, e Entry, size int) (string, error) {
	h := sha1.Sum(fmt.Appendf(nil, "%s|%s|%d", src, e.Version, size))
	key := hex.EncodeToString(h[:])
	for _, ext := range []string{".jpg", ".png"} {
		if p := filepath.Join(t.dir, key+ext); fileExists(p) {
			return p, nil
		}
	}

	t.mu.Lock()
	if t.failed[key] {
		t.mu.Unlock()
		return "", errNoThumb
	}
	if c, ok := t.inflight[key]; ok {
		t.mu.Unlock()
		<-c.done
		return c.path, c.err
	}
	c := &thumbCall{done: make(chan struct{})}
	t.inflight[key] = c
	t.mu.Unlock()

	t.sem <- struct{}{}
	c.path, c.err = t.make(src, e.Kind, key, size)
	<-t.sem

	t.mu.Lock()
	delete(t.inflight, key)
	if c.err != nil {
		t.failed[key] = true
	}
	t.mu.Unlock()
	close(c.done)
	return c.path, c.err
}

func (t *Thumbs) make(src, kind, key string, size int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := filepath.Join(t.dir, key+".jpg")
	if runtime.GOOS != "darwin" {
		// Go for the common image formats, ffmpeg (if installed) for videos, HEIC and the rest.
		if kind == "image" && canDecodeInGo(src) {
			if err := goThumb(src, out, size); err == nil {
				return out, nil
			}
		}
		if err := ffmpegThumb(ctx, src, out, size, kind == "video"); err != nil {
			return "", err
		}
		return out, nil
	}
	if kind == "image" {
		tmp := out + ".tmp.jpg"
		cmd := exec.CommandContext(ctx, "sips", "-s", "format", "jpeg", "-s", "formatOptions", "82",
			"-Z", fmt.Sprint(size), src, "--out", tmp)
		if err := cmd.Run(); err == nil && fileExists(tmp) {
			return out, os.Rename(tmp, out)
		}
		os.Remove(tmp)
	}
	// Quick Look writes <name>.png into the folder it's given.
	work, err := os.MkdirTemp(t.dir, "ql-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	cmd := exec.CommandContext(ctx, "qlmanage", "-t", "-s", fmt.Sprint(size), "-o", work, src)
	if err := cmd.Run(); err != nil {
		return "", errNoThumb
	}
	made := filepath.Join(work, filepath.Base(src)+".png")
	if !fileExists(made) {
		return "", errNoThumb
	}
	png := filepath.Join(t.dir, key+".png")
	return png, os.Rename(made, png)
}

// Prune removes cached pictures made over a month ago; any still needed are made again.
func (t *Thumbs) Prune() {
	items, _ := os.ReadDir(t.dir)
	for _, it := range items {
		if info, err := it.Info(); err == nil && time.Since(info.ModTime()) > 30*24*time.Hour {
			os.RemoveAll(filepath.Join(t.dir, it.Name()))
		}
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
