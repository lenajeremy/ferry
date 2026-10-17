package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Device is a phone or browser paired with this computer. Pairing (with the code) gives each one
// its own token, so one can be removed without changing the code, and the code can change without
// disconnecting anyone.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`               // "iPhone", "Pixel 9", "Safari on iPhone"
	Kind     string    `json:"kind"`               // what deviceFromAgent says: iPhone, iPad, Android, Mac, PC, Linux
	App      bool      `json:"app"`                // the Ferry app, rather than a browser
	Computer bool      `json:"computer,omitempty"` // another computer running Ferry (paired both ways)
	Hash     string    `json:"hash"`               // SHA-256 of its token; the token itself isn't kept
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"lastSeen"`
}

// Devices are kept in devices.json in Ferry's data folder (in memory only when path is "").
type Devices struct {
	mu    sync.Mutex
	path  string
	list  []Device
	saved time.Time
}

func OpenDevices(dir string) (*Devices, error) {
	d := &Devices{path: filepath.Join(dir, "devices.json")}
	b, err := os.ReadFile(d.path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &d.list); err != nil {
		return nil, fmt.Errorf("devices.json: %w", err)
	}
	return d, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Add pairs a device and gives back its token, which only the device keeps.
func (d *Devices) Add(name, kind string, app bool) (Device, string, error) {
	return d.add(name, kind, app, false)
}

func (d *Devices) add(name, kind string, app, computer bool) (Device, string, error) {
	token := randomString(32)
	now := time.Now().UTC().Truncate(time.Second)
	dev := Device{ID: randomString(6), Name: name, Kind: kind, App: app, Computer: computer, Hash: hashToken(token), Paired: now, LastSeen: now}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.list = append(d.list, dev)
	return dev, token, d.saveLocked()
}

// Find is the device a token belongs to, noting that it was seen.
func (d *Devices) Find(token string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	h := hashToken(token)
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.list {
		if subtle.ConstantTimeCompare([]byte(d.list[i].Hash), []byte(h)) != 1 {
			continue
		}
		if now := time.Now().UTC(); now.Sub(d.list[i].LastSeen) > time.Minute {
			d.list[i].LastSeen = now.Truncate(time.Second)
			if now.Sub(d.saved) > time.Minute {
				d.saveLocked()
			}
		}
		return d.list[i], true
	}
	return Device{}, false
}

// Remove unpairs a device: its token stops working.
func (d *Devices) Remove(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.list)
	d.list = slices.DeleteFunc(d.list, func(x Device) bool { return x.ID == id })
	if len(d.list) == n {
		return false
	}
	d.saveLocked()
	return true
}

// Rename gives a device the name it turned out to have.
func (d *Devices) Rename(id, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.list {
		if d.list[i].ID == id {
			d.list[i].Name = name
			d.saveLocked()
		}
	}
}

// List is every paired device, the most recently seen first.
func (d *Devices) List() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := slices.Clone(d.list)
	slices.SortFunc(out, func(a, b Device) int { return b.LastSeen.Compare(a.LastSeen) })
	return out
}

func (d *Devices) saveLocked() error {
	d.saved = time.Now()
	if d.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(d.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// cleanLabel keeps a name a device gives itself short and printable.
func cleanLabel(name, fallback string) string {
	name = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)), " ")
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	if name == "" {
		return fallback
	}
	return name
}

// browserName names a paired browser by what its user agent says: "Safari on iPhone".
func browserName(ua string) string {
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgiOS") || strings.Contains(ua, "EdgA"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox") || strings.Contains(ua, "FxiOS"):
		browser = "Firefox"
	case strings.Contains(ua, "SamsungBrowser"):
		browser = "Samsung Internet"
	case strings.Contains(ua, "Chrome") || strings.Contains(ua, "CriOS"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari"):
		browser = "Safari"
	}
	return browser + " on " + deviceFromAgent(ua)
}

// computerID is this computer's own id, made once: phones use it to tell computers apart.
func computerID(appDir string) (string, error) {
	p := filepath.Join(appDir, "id")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 8 {
		return strings.TrimSpace(string(b)), nil
	}
	id := randomString(9)
	return id, os.WriteFile(p, []byte(id), 0o600)
}
