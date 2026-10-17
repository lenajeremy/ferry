package qr

import (
	"errors"
	"strings"
	"testing"
)

func TestPicksTheSmallestVersion(t *testing.T) {
	for _, tc := range []struct {
		text string
		size int
	}{
		{"hello", 21},                 // version 1 holds 14 bytes at level M
		{strings.Repeat("a", 14), 21}, // exactly full
		{strings.Repeat("a", 15), 25}, // version 2
		{"http://Ferry-Test-MacBook-Pro4.local:7070/?k=k7m2q9xa", 33}, // 54 bytes: version 4 (62)
		{strings.Repeat("a", 213), 57},                                // version 10's 216 data codewords hold 213 bytes
	} {
		c, err := Encode(tc.text)
		if err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}
		if c.Size != tc.size {
			t.Errorf("%d bytes: size %d, want %d", len(tc.text), c.Size, tc.size)
		}
	}
}

func TestTooLong(t *testing.T) {
	if _, err := Encode(strings.Repeat("a", 214)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
}

// Reed–Solomon check against the worked example in ISO/IEC 18004 Annex I (version 1-M, "01234567").
func TestErrorCorrectionMatchesTheStandard(t *testing.T) {
	data := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	want := []byte{0xA5, 0x24, 0xD4, 0xC1, 0xED, 0x36, 0xC7, 0x87, 0x2C, 0x55}
	got := rsRemainder(data, rsDivisor(10))
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ecc = % X, want % X", got, want)
		}
	}
}

func TestFindersAndTiming(t *testing.T) {
	c, _ := Encode("ferry")
	for _, p := range [][2]int{{0, 0}, {c.Size - 7, 0}, {0, c.Size - 7}} {
		for i := range 7 {
			if !c.Dark(p[0]+i, p[1]) || !c.Dark(p[0], p[1]+i) {
				t.Fatalf("finder at %v isn't dark round its edge", p)
			}
		}
		if c.Dark(p[0]+1, p[1]+1) || !c.Dark(p[0]+3, p[1]+3) {
			t.Fatalf("finder at %v has the wrong rings", p)
		}
	}
	for i := 8; i < c.Size-8; i++ {
		if c.Dark(i, 6) != (i%2 == 0) || c.Dark(6, i) != (i%2 == 0) {
			t.Fatalf("timing pattern broken at %d", i)
		}
	}
	if !c.Dark(8, c.Size-8) {
		t.Fatal("the dark module is light")
	}
}
