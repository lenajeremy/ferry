package qr

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Writes PNGs for an outside decoder when FERRY_QR_PNG names a folder (see README).
func TestWritePNGs(t *testing.T) {
	dir := os.Getenv("FERRY_QR_PNG")
	if dir == "" {
		t.Skip("set FERRY_QR_PNG to write sample codes")
	}
	texts := []string{"hello", "http://Ferry-Test-MacBook-Pro4.local:7070/?k=k7m2q9xa",
		"https://192.168.1.20:7071/?k=abcdefgh", strings.Repeat("Ferry ", 20), strings.Repeat("x", 150), strings.Repeat("z", 213)}
	for i, text := range texts {
		c, err := Encode(text)
		if err != nil {
			t.Fatal(err)
		}
		scale := 8
		n := (c.Size + 8) * scale
		img := image.NewGray(image.Rect(0, 0, n, n))
		for y := range n {
			for x := range n {
				v := uint8(255)
				if c.Dark(x/scale-4, y/scale-4) {
					v = 0
				}
				img.SetGray(x, y, color.Gray{v})
			}
		}
		f, _ := os.Create(filepath.Join(dir, fmt.Sprintf("%d.png", i)))
		png.Encode(f, img)
		f.Close()
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.txt", i)), []byte(text), 0o644)
	}
}
