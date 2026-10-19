package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ferry/server"

	"github.com/wailsapp/wails/v2/pkg/options"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the window's side of Ferry: it starts Ferry and hands the page its address.
type App struct {
	ctx  context.Context
	cfg  server.Config
	base string

	mu      sync.Mutex
	stop    func()   // stops the Ferry this app started (nil when it uses one already running)
	ready   bool     // Ferry is up, so files can go
	waiting []string // files the app was opened with (Send to › Ferry, Send with Ferry), sent once Ferry is up
}

func NewApp() *App {
	cfg := server.DefaultConfig()
	// For trying the app beside a Ferry that's already running (screenshots, development).
	if p, err := strconv.Atoi(os.Getenv("FERRY_PORT")); err == nil {
		cfg.Port, cfg.TLSPort = p, p+1
	}
	if d := os.Getenv("FERRY_DATA"); d != "" {
		cfg.DataDir = d
	}
	if d := os.Getenv("FERRY_DIR"); d != "" {
		cfg.Dir = d
	}
	cfg.Name = os.Getenv("FERRY_NAME")
	if os.Getenv("FERRY_BONJOUR") == "0" {
		cfg.Bonjour = false
	}
	wd, _ := os.Getwd()
	return &App{cfg: cfg, base: fmt.Sprintf("http://localhost:%d", cfg.Port), waiting: filesIn(os.Args[1:], wd)}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go enableShareMenu(a.cfg.DataDir)
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		a.stop()
	}
}

// secondInstance: Ferry was opened again while it's open, maybe with files to send (Send to › Ferry on
// Windows, Send with Ferry in a Linux file manager). The window comes forward and the files go.
func (a *App) secondInstance(data options.SecondInstanceData) {
	wruntime.WindowUnminimise(a.ctx)
	wruntime.Show(a.ctx)
	if files := filesIn(data.Args, data.WorkingDirectory); len(files) > 0 {
		// Not on this goroutine: it's Wails' own, and Start can hold the lock while Ferry starts.
		go func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if !a.ready {
				a.waiting = append(a.waiting, files...)
				return
			}
			go a.send(files)
		}()
	}
}

// Start makes sure Ferry is running and gives the address of its page, which the window then opens.
// If Ferry already runs on this computer (started at login, or in a terminal), the window shows that one.
// Files the app was opened with go once it's up.
func (a *App) Start() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop == nil && !server.Running(a.cfg.Port) {
		if err := a.run(); err != nil {
			return "", err
		}
	}
	a.ready = true
	if len(a.waiting) > 0 {
		go a.send(a.waiting)
		a.waiting = nil
	}
	return a.base + "/?app", nil
}

// run starts Ferry inside the app, which stops it when it quits. The caller holds a.mu.
func (a *App) run() error {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	cfg := a.cfg
	cfg.Ready = func(*server.Server) { close(ready) }
	go func() { done <- server.Run(ctx, cfg) }()

	select {
	case <-ready:
		// Wait for Ferry to stop announcing itself on the network before the app quits.
		a.stop = func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
		return nil
	case err := <-done:
		cancel()
		if errors.Is(err, server.ErrPortInUse) {
			return fmt.Errorf("another program is using port %d, which Ferry needs. Quit it, then try again", a.cfg.Port)
		}
		return err
	case <-time.After(30 * time.Second):
		cancel()
		return errors.New("Ferry took too long to start")
	}
}

// SendFiles picks files with the system's file dialog and sends them to the phones.
func (a *App) SendFiles() {
	paths, err := wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Send to your phone"})
	if err != nil || len(paths) == 0 {
		return
	}
	go a.send(paths)
}

// send sends files to the phones one after another, the same way a drop on the page does. Folders
// can't go (as on the page), so they're skipped, with a word about it.
func (a *App) send(paths []string) {
	var folders []string
	sent := 0
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			folders = append(folders, filepath.Base(p))
			continue
		}
		if err := a.upload(p); err != nil {
			log.Printf("send %s: %v", filepath.Base(p), err)
			a.alert(wruntime.ErrorDialog, "Couldn't send "+filepath.Base(p), err.Error())
			return
		}
		sent++
	}
	switch {
	case len(folders) == 0:
	case sent == 0:
		a.alert(wruntime.ErrorDialog, "Couldn't send "+strings.Join(folders, ", "),
			"Ferry sends files, photos and videos, not folders. Compress the folder first, then send that.")
	default:
		a.alert(wruntime.InfoDialog, fmt.Sprintf("Sent %d of %d", sent, len(paths)),
			"Ferry sends files, photos and videos, not folders, so it left out "+strings.Join(folders, ", ")+".")
	}
}

func (a *App) alert(kind wruntime.DialogType, title, message string) {
	wruntime.MessageDialog(a.ctx, wruntime.MessageDialogOptions{Type: kind, Title: title, Message: message})
}

// filesIn picks out the files and folders the app was opened with: Send to › Ferry (Windows) and
// Send with Ferry (Linux file managers) run it with their paths. Options and names that aren't
// there are ignored; relative paths are from dir, where it was opened.
func filesIn(args []string, dir string) []string {
	var paths []string
	for _, arg := range args {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if !filepath.IsAbs(arg) {
			arg = filepath.Join(dir, arg)
		}
		if _, err := os.Stat(arg); err == nil {
			paths = append(paths, arg)
		}
	}
	return paths
}

func (a *App) upload(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequest(http.MethodPut, a.base+"/api/upload?name="+url.QueryEscape(filepath.Base(path)), f)
	if err != nil {
		return err
	}
	req.Header.Set("X-Ferry", "1")
	return a.do(req)
}

// OpenFolder shows the shared folder in Finder, File Explorer or Files.
func (a *App) OpenFolder() {
	req, _ := http.NewRequest(http.MethodPost, a.base+"/api/open-folder", nil)
	req.Header.Set("X-Ferry", "1")
	if err := a.do(req); err != nil {
		log.Printf("open the folder: %v", err)
	}
}

// OpenInBrowser opens Ferry's page in the usual browser, for anyone who prefers a tab.
func (a *App) OpenInBrowser() { wruntime.BrowserOpenURL(a.ctx, a.base+"/") }

func (a *App) do(req *http.Request) error {
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode >= 300 {
		return fmt.Errorf("Ferry said %s", r.Status)
	}
	return nil
}
