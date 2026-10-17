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

	mu   sync.Mutex
	stop func() // stops the Ferry this app started (nil when it uses one already running)
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
	return &App{cfg: cfg, base: fmt.Sprintf("http://localhost:%d", cfg.Port)}
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		a.stop()
	}
}

func (a *App) secondInstance(options.SecondInstanceData) {
	wruntime.WindowUnminimise(a.ctx)
	wruntime.Show(a.ctx)
}

// Start makes sure Ferry is running and gives the address of its page, which the window then opens.
// If Ferry already runs on this computer (started at login, or in a terminal), the window shows that one.
func (a *App) Start() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil || server.Running(a.cfg.Port) {
		return a.base + "/?app", nil
	}
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
		return a.base + "/?app", nil
	case err := <-done:
		cancel()
		if errors.Is(err, server.ErrPortInUse) {
			return "", fmt.Errorf("another program is using port %d, which Ferry needs. Quit it, then try again", a.cfg.Port)
		}
		return "", err
	case <-time.After(30 * time.Second):
		cancel()
		return "", errors.New("Ferry took too long to start")
	}
}

// SendFiles picks files with the system's file dialog and sends them to the phones, the same way
// a drop on the page does.
func (a *App) SendFiles() {
	paths, err := wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Send to your phone"})
	if err != nil || len(paths) == 0 {
		return
	}
	go func() {
		for _, p := range paths {
			if err := a.upload(p); err != nil {
				log.Printf("send %s: %v", filepath.Base(p), err)
				wruntime.MessageDialog(a.ctx, wruntime.MessageDialogOptions{
					Type: wruntime.ErrorDialog, Title: "Couldn't send " + filepath.Base(p), Message: err.Error(),
				})
				return
			}
		}
	}()
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
