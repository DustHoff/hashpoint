//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
)

// trayIcon returns the bytes of a 16x16 32-bit ICO with the Hashpoint accent
// colour as background and a small "H" mark drawn in white. Windows requires
// real ICO bytes for systray.SetIcon — a PNG would not be rendered.
func trayIcon() []byte {
	const size = 16
	const bpp = 32

	bg := [4]byte{0xFF, 0x8C, 0x4F, 0xFF} // BGRA accent (#4f8cff)
	fg := [4]byte{0xFF, 0xFF, 0xFF, 0xFF} // BGRA white

	// 16x16 mask of an "H" — true = foreground.
	glyph := [size][size]bool{}
	for y := 3; y < 13; y++ {
		glyph[y][4] = true
		glyph[y][5] = true
		glyph[y][10] = true
		glyph[y][11] = true
	}
	for x := 4; x < 12; x++ {
		glyph[7][x] = true
		glyph[8][x] = true
	}

	xor := make([]byte, 0, size*size*4)
	// BMP rows are bottom-up.
	for row := size - 1; row >= 0; row-- {
		for col := 0; col < size; col++ {
			px := bg
			if glyph[row][col] {
				px = fg
			}
			xor = append(xor, px[0], px[1], px[2], px[3])
		}
	}
	// AND mask: 16*16/8 = 32 bytes, all zero (= fully opaque).
	andMask := make([]byte, size*size/8)

	// BITMAPINFOHEADER + pixel + mask
	var bmp bytes.Buffer
	writeLE := func(v any) { _ = binary.Write(&bmp, binary.LittleEndian, v) }
	writeLE(uint32(40))         // size
	writeLE(int32(size))        // width
	writeLE(int32(size * 2))    // height (= 2 * actual because of XOR+AND)
	writeLE(uint16(1))          // planes
	writeLE(uint16(bpp))        // bpp
	writeLE(uint32(0))          // compression
	writeLE(uint32(len(xor) + len(andMask)))
	writeLE(int32(0))   // xres
	writeLE(int32(0))   // yres
	writeLE(uint32(0))  // clrUsed
	writeLE(uint32(0))  // clrImportant
	bmp.Write(xor)
	bmp.Write(andMask)

	// ICONDIR + ICONDIRENTRY
	var ico bytes.Buffer
	writeIco := func(v any) { _ = binary.Write(&ico, binary.LittleEndian, v) }
	writeIco(uint16(0))         // reserved
	writeIco(uint16(1))         // type=icon
	writeIco(uint16(1))         // count
	writeIco(uint8(size))       // width
	writeIco(uint8(size))       // height
	writeIco(uint8(0))          // palette colours
	writeIco(uint8(0))          // reserved
	writeIco(uint16(1))         // planes
	writeIco(uint16(bpp))       // bpp
	writeIco(uint32(bmp.Len())) // image size
	writeIco(uint32(22))        // offset
	ico.Write(bmp.Bytes())
	return ico.Bytes()
}
