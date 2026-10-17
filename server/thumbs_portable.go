package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Thumbnails without the Mac's tools: Go's own decoders for JPEG, PNG and GIF (turned upright from
// the photo's EXIF orientation), and ffmpeg, when it's installed, for videos and everything else.

func goThumb(src, out string, size int) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	img = upright(img, jpegOrientation(data))
	return writeJPEG(shrink(img, size), out)
}

func ffmpegThumb(ctx context.Context, src, out string, size int, video bool) error {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errNoThumb
	}
	scale := fmt.Sprintf("scale='if(gt(iw,ih),min(%d,iw),-2)':'if(gt(iw,ih),-2,min(%d,ih))'", size, size)
	filter := scale
	if video {
		filter = "thumbnail," + scale // a representative frame from the first few seconds
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error", "-y", "-i", src, "-frames:v", "1", "-vf", filter, "-q:v", "4", out)
	if err := cmd.Run(); err != nil || !fileExists(out) {
		return errNoThumb
	}
	return nil
}

func canDecodeInGo(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	}
	return false
}

// shrink scales an image down so its long side is at most size, averaging the pixels each new pixel covers.
func shrink(src image.Image, size int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= size && h <= size {
		return src
	}
	scale := float64(size) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			var r, g, bl, n uint64
			// Sample a grid of up to 4×4 points in the source block: quick, and smooth enough at thumbnail size.
			for sy := y0; sy < max(y1, y0+1); sy += max(1, (y1-y0)/4) {
				for sx := x0; sx < max(x1, x0+1); sx += max(1, (x1-x0)/4) {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), 255
		}
	}
	return dst
}

func writeJPEG(img image.Image, out string) error {
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = jpeg.Encode(f, img, &jpeg.Options{Quality: 82})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, out)
}

// jpegOrientation reads the EXIF orientation (1–8) from a JPEG; 1 when there's none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker, size := data[i+1], int(binary.BigEndian.Uint16(data[i+2:i+4]))
		if marker == 0xE1 && i+4+size <= len(data) && bytes.HasPrefix(data[i+4:], []byte("Exif\x00\x00")) {
			return tiffOrientation(data[i+10 : i+2+size])
		}
		if marker == 0xDA {
			return 1
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder = binary.BigEndian
	if string(t[:2]) == "II" {
		bo = binary.LittleEndian
	}
	ifd := int(bo.Uint32(t[4:8]))
	if ifd+2 > len(t) {
		return 1
	}
	for n, i := int(bo.Uint16(t[ifd:])), 0; i < n; i++ {
		e := ifd + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
		}
	}
	return 1
}

// upright applies an EXIF orientation, so a phone's portrait photo isn't shown on its side.
func upright(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
