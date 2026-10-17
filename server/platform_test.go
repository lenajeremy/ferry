package server

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One computer's announcer hears another's answer and lists it (Windows and Linux find each other this way).
func TestMDNSHearsOtherComputers(t *testing.T) {
	other := &mdnsResponder{instance: "Studio PC", host: "studio-pc-ferry.local.", port: 7070,
		txt: []string{"v=1", "port=7070", "id=studio1", "tls=7071", "fp=abc"}, ips: func() []net.IP { return []net.IP{net.ParseIP("192.168.0.31")} }}
	changes := 0
	f := newFinder("self", func() { changes++ })
	me := &mdnsResponder{instance: "Alex's PC", finder: f}
	me.heard(other.answer(true, true, true))
	list := f.list()
	if len(list) != 1 || list[0].ID != "studio1" || list[0].Name != "Studio PC" || list[0].Host != "192.168.0.31" ||
		list[0].Port != 7070 || list[0].TLSPort != 7071 || list[0].Fingerprint != "abc" || changes != 1 {
		t.Fatalf("heard: %+v (changes %d)", list, changes)
	}
	// Hearing itself (its own id) lists nothing.
	f2 := newFinder("studio1", func() {})
	(&mdnsResponder{finder: f2}).heard(other.answer(true, true, true))
	if len(f2.list()) != 0 {
		t.Fatalf("listed itself: %+v", f2.list())
	}
}

func TestMDNSAnswersForFerry(t *testing.T) {
	m := &mdnsResponder{instance: "Alex's PC", host: "alex-s-pc-ferry.local.", port: 7070,
		txt: []string{"v=1", "port=7070"}, ips: func() []net.IP { return []net.IP{net.ParseIP("192.168.0.20")} }}
	if ptr, _, _, _ := m.wants(query("_ferry._tcp.local.", dnsPTR)); !ptr {
		t.Fatal("didn't answer a browse for _ferry._tcp")
	}
	if _, srv, _, _ := m.wants(query("Alex's PC._ferry._tcp.local.", dnsSRV)); !srv {
		t.Fatal("didn't answer for its own instance")
	}
	if _, _, a, _ := m.wants(query("alex-s-pc-ferry.local.", dnsA)); !a {
		t.Fatal("didn't answer for its host name")
	}
	if p, s, a, _ := m.wants(query("_airplay._tcp.local.", dnsPTR)); p || s || a {
		t.Fatal("answered someone else's service")
	}
	// The answer reads back: PTR → instance, SRV → port and host, TXT, A.
	msg := m.answer(true, true, true)
	if n := binary.BigEndian.Uint16(msg[6:8]); n != 4 {
		t.Fatalf("%d answers, want 4", n)
	}
	off := 12
	var seen []string
	for i := 0; i < 4; i++ {
		name, next, ok := readName(msg, off)
		if !ok {
			t.Fatal("unreadable record name")
		}
		typ := binary.BigEndian.Uint16(msg[next:])
		l := int(binary.BigEndian.Uint16(msg[next+8:]))
		data := msg[next+10 : next+10+l]
		switch typ {
		case dnsPTR:
			target, _, _ := readName(msg, next+10)
			seen = append(seen, "PTR "+name+" → "+target)
		case dnsSRV:
			target, _, _ := readName(msg, next+16)
			seen = append(seen, "SRV "+target+" "+string(rune('0'+binary.BigEndian.Uint16(data[4:])/1000)))
		case dnsTXT:
			seen = append(seen, "TXT "+string(data[1:1+data[0]]))
		case dnsA:
			seen = append(seen, "A "+net.IP(data).String())
		}
		off = next + 10 + l
	}
	got := strings.Join(seen, " | ")
	want := "PTR _ferry._tcp.local. → Alex's PC._ferry._tcp.local. | SRV alex-s-pc-ferry.local. 7 | TXT v=1 | A 192.168.0.20"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

// A JPEG from a phone held upright is stored sideways with orientation 6; its thumbnail must not be.
func TestThumbnailsTurnPhotosUpright(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 400, 200)) // stored landscape…
	for x := 0; x < 400; x++ {
		for y := 0; y < 200; y++ {
			img.Set(x, y, color.RGBA{uint8(x / 2), 80, 160, 255})
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	// …with an EXIF block saying "rotate 90° clockwise" (orientation 6), little-endian TIFF.
	tiff := []byte("II*\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00\x00")
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := append([]byte{0xFF, 0xE1}, byte((len(app1)+2)>>8), byte(len(app1)+2))
	data := append(append([]byte{0xFF, 0xD8}, append(seg, app1...)...), buf.Bytes()[2:]...)
	src := filepath.Join(dir, "photo.jpg")
	os.WriteFile(src, data, 0o644)
	if o := jpegOrientation(data); o != 6 {
		t.Fatalf("orientation %d, want 6", o)
	}
	out := filepath.Join(dir, "thumb.jpg")
	if err := goThumb(src, out, 100); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(out)
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 50 || cfg.Height != 100 {
		t.Fatalf("thumbnail %d×%d, want 50×100 (upright, long side 100)", cfg.Width, cfg.Height)
	}
}

func TestFreedesktopTrash(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dir := t.TempDir()
	p := filepath.Join(dir, "holiday photo.jpg")
	os.WriteFile(p, []byte("x"), 0o644)
	if err := freedesktopTrash(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("still in the folder")
	}
	if b, err := os.ReadFile(filepath.Join(data, "Trash", "files", "holiday photo.jpg")); err != nil || string(b) != "x" {
		t.Fatal("not in the Trash")
	}
	info, _ := os.ReadFile(filepath.Join(data, "Trash", "info", "holiday photo.jpg.trashinfo"))
	if !strings.Contains(string(info), "Path="+strings.ReplaceAll(filepath.ToSlash(p), " ", "%20")) {
		t.Fatalf("trashinfo doesn't say where it came from:\n%s", info)
	}
}
