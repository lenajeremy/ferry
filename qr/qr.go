// Package qr draws QR codes (byte mode, error correction level M, versions 1–10), enough for a
// link with an access code. It follows ISO/IEC 18004; the layout steps mirror Nayuki's
// reference implementation.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a square of modules; true is dark.
type Code struct {
	Size    int
	modules [][]bool
}

// Dark reports whether the module at column x, row y is dark. Outside the code is light.
func (c *Code) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.modules[y][x]
}

// block layout for level M: error correction codewords per block, then the data codewords of
// each block (group 1 blocks first, then group 2).
type layout struct {
	ecPerBlock int
	blocks     []int
}

var levelM = [...]layout{
	1:  {10, []int{16}},
	2:  {16, []int{28}},
	3:  {26, []int{44}},
	4:  {18, []int{32, 32}},
	5:  {24, []int{43, 43}},
	6:  {16, []int{27, 27, 27, 27}},
	7:  {18, []int{31, 31, 31, 31}},
	8:  {22, []int{38, 38, 39, 39}},
	9:  {22, []int{36, 36, 36, 37, 37}},
	10: {26, []int{43, 43, 43, 43, 44}},
}

var alignment = [...][]int{
	1: nil, 2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34},
	7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50},
}

// ErrTooLong means the text doesn't fit in a version 10 code.
var ErrTooLong = errors.New("qr: text too long")

// Encode makes the smallest code that holds text.
func Encode(text string) (*Code, error) {
	data := []byte(text)
	for v := 1; v < len(levelM); v++ {
		capacity := 0
		for _, n := range levelM[v].blocks {
			capacity += n
		}
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(data) > capacity*8 {
			continue
		}
		return build(v, data, countBits, capacity), nil
	}
	return nil, fmt.Errorf("%w (%d bytes)", ErrTooLong, len(data))
}

func build(version int, data []byte, countBits, capacity int) *Code {
	// Mode, length, the bytes, a terminator, then padding to the data capacity.
	var bits bitBuffer
	bits.append(0b0100, 4)
	bits.append(len(data), countBits)
	for _, b := range data {
		bits.append(int(b), 8)
	}
	bits.append(0, min(4, capacity*8-len(bits)))
	bits.append(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capacity*8; pad ^= 0xEC ^ 0x11 {
		bits.append(pad, 8)
	}
	codewords := bits.bytes()

	c := newCode(version)
	c.drawFunctionPatterns(version)
	c.drawCodewords(interleave(codewords, levelM[version]))
	c.applyBestMask(version)
	return &Code{Size: c.size, modules: c.modules}
}

// interleave splits the data into blocks, adds each block's error correction, and interleaves
// the data codewords then the error correction codewords.
func interleave(data []byte, l layout) []byte {
	divisor := rsDivisor(l.ecPerBlock)
	var blocks, ecc [][]byte
	maxData := 0
	for _, n := range l.blocks {
		block := data[:n]
		data = data[n:]
		blocks = append(blocks, block)
		ecc = append(ecc, rsRemainder(block, divisor))
		maxData = max(maxData, n)
	}
	var out []byte
	for i := 0; i < maxData; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < l.ecPerBlock; i++ {
		for _, e := range ecc {
			out = append(out, e[i])
		}
	}
	return out
}

type bitBuffer []bool

func (b *bitBuffer) append(value, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, (value>>i)&1 == 1)
	}
}

func (b bitBuffer) bytes() []byte {
	out := make([]byte, len(b)/8)
	for i, bit := range b {
		if bit {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

// MARK: Reed–Solomon over GF(256), polynomial 0x11D

func gfMul(x, y byte) byte {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= int((y>>i)&1) * int(x)
	}
	return byte(z)
}

func rsDivisor(degree int) []byte {
	result := make([]byte, degree)
	result[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range result {
			result[j] = gfMul(result[j], root)
			if j+1 < len(result) {
				result[j] ^= result[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return result
}

func rsRemainder(data, divisor []byte) []byte {
	result := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i, d := range divisor {
			result[i] ^= gfMul(d, factor)
		}
	}
	return result
}

// MARK: Layout

type grid struct {
	size       int
	modules    [][]bool
	isFunction [][]bool
}

func newCode(version int) *grid {
	size := version*4 + 17
	g := &grid{size: size, modules: make([][]bool, size), isFunction: make([][]bool, size)}
	for i := range size {
		g.modules[i] = make([]bool, size)
		g.isFunction[i] = make([]bool, size)
	}
	return g
}

func (g *grid) set(x, y int, dark bool) {
	g.modules[y][x] = dark
	g.isFunction[y][x] = true
}

func (g *grid) drawFunctionPatterns(version int) {
	for i := range g.size {
		g.set(6, i, i%2 == 0)
		g.set(i, 6, i%2 == 0)
	}
	for _, p := range [][2]int{{3, 3}, {g.size - 4, 3}, {3, g.size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := p[0]+dx, p[1]+dy
				if x >= 0 && y >= 0 && x < g.size && y < g.size {
					d := max(abs(dx), abs(dy))
					g.set(x, y, d != 2 && d != 4)
				}
			}
		}
	}
	pos := alignment[version]
	for i, cx := range pos {
		for j, cy := range pos {
			last := len(pos) - 1
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					g.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	g.drawFormat(0) // reserves the format areas; redrawn with the chosen mask
	if version >= 7 {
		rem := version
		for range 12 {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := version<<12 | rem
		for i := range 18 {
			dark := (bits>>i)&1 == 1
			a, b := g.size-11+i%3, i/3
			g.set(a, b, dark)
			g.set(b, a, dark)
		}
	}
}

// drawFormat writes level M and the mask, twice, plus the always-dark module.
func (g *grid) drawFormat(mask int) {
	data := 0b00<<3 | mask // level M is 00
	rem := data
	for range 10 {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		g.set(8, i, bit(i))
	}
	g.set(8, 7, bit(6))
	g.set(8, 8, bit(7))
	g.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		g.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		g.set(g.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		g.set(8, g.size-15+i, bit(i))
	}
	g.set(8, g.size-8, true)
}

// drawCodewords fills the data area in the two-column zigzag, from the bottom right.
func (g *grid) drawCodewords(data []byte) {
	i := 0
	for right := g.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := range g.size {
			for j := range 2 {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = g.size - 1 - vert
				}
				if !g.isFunction[y][x] && i < len(data)*8 {
					g.modules[y][x] = (data[i>>3]>>(7-i&7))&1 == 1
					i++
				}
			}
		}
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

func (g *grid) applyMask(mask int) {
	for y := range g.size {
		for x := range g.size {
			if !g.isFunction[y][x] && maskBit(mask, x, y) {
				g.modules[y][x] = !g.modules[y][x]
			}
		}
	}
}

func (g *grid) applyBestMask(version int) {
	best, bestScore := 0, -1
	for mask := range 8 {
		g.applyMask(mask)
		g.drawFormat(mask)
		if s := g.penalty(); bestScore < 0 || s < bestScore {
			best, bestScore = mask, s
		}
		g.applyMask(mask) // XOR again undoes it
	}
	g.applyMask(best)
	g.drawFormat(best)
}

// penalty scores how hard the code is to read (lower is better): long runs, 2×2 blocks,
// finder-like patterns and an unbalanced dark share.
func (g *grid) penalty() int {
	score := 0
	line := func(get func(i int) bool) {
		run := 1
		for i := 1; i < g.size; i++ {
			if get(i) == get(i-1) {
				run++
				continue
			}
			if run >= 5 {
				score += run - 2
			}
			run = 1
		}
		if run >= 5 {
			score += run - 2
		}
		// 1:1:3:1:1 with four light modules on either side.
		for i := 0; i+7 <= g.size; i++ {
			at := func(k int) bool { return k >= 0 && k < g.size && get(k) }
			core := at(i) && !at(i+1) && at(i+2) && at(i+3) && at(i+4) && !at(i+5) && at(i+6)
			if !core {
				continue
			}
			before := !at(i-1) && !at(i-2) && !at(i-3) && !at(i-4)
			after := !at(i+7) && !at(i+8) && !at(i+9) && !at(i+10)
			if before || after {
				score += 40
			}
		}
	}
	for y := range g.size {
		line(func(i int) bool { return g.modules[y][i] })
	}
	for x := range g.size {
		line(func(i int) bool { return g.modules[i][x] })
	}
	dark := 0
	for y := range g.size {
		for x := range g.size {
			if g.modules[y][x] {
				dark++
			}
			if x+1 < g.size && y+1 < g.size {
				c := g.modules[y][x]
				if c == g.modules[y][x+1] && c == g.modules[y+1][x] && c == g.modules[y+1][x+1] {
					score += 3
				}
			}
		}
	}
	total := g.size * g.size
	k := (abs(dark*20-total*10)+total-1)/total - 1
	score += max(k, 0) * 10
	return score
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// MARK: Output

// SVG draws the code with a quiet zone of four modules, dark on light.
func (c *Code) SVG(dark, light string) string {
	n := c.Size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/><path fill="%s" d="`, n, n, light, dark)
	for y := range c.Size {
		for x := range c.Size {
			if c.modules[y][x] {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// Terminal draws the code with half blocks, two rows per line, with a four-module quiet zone.
// Blocks print in the text colour, so on a dark terminal (light text) pass invert: the light
// modules are drawn and the code still reads dark on light.
func (c *Code) Terminal(invert bool) string {
	var b strings.Builder
	dark := func(x, y int) bool { return c.Dark(x, y) != invert }
	for y := -4; y < c.Size+4; y += 2 {
		b.WriteString("  ")
		for x := -4; x < c.Size+4; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
