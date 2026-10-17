package images

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/draw"
)

func TestSquare(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		w, h, size int
		want       int
	}{
		{"landscape", 400, 200, 100, 100},
		{"portrait", 200, 400, 100, 100},
		{"smaller than size", 60, 80, 100, 60},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			img := image.NewRGBA(image.Rect(0, 0, tt.w, tt.h))
			// Mark the middle, which the square keeps.
			img.Set(tt.w/2, tt.h/2, color.RGBA{R: 255, A: 255})
			got := Square(img, tt.size)
			if b := got.Bounds(); b.Dx() != tt.want || b.Dy() != tt.want {
				t.Errorf("Square(%dx%d, %d) is %dx%d, want %dx%d", tt.w, tt.h, tt.size, b.Dx(), b.Dy(), tt.want, tt.want)
			}
		})
	}
}

func TestDecodeRejectsNonImages(t *testing.T) {
	t.Parallel()
	if err := Decode(t.Context(), []byte("not an image"), ignore); err != ErrNotImage {
		t.Errorf("Decode returned %v, want ErrNotImage", err)
	}
}

// TestScaleDownLargeImage scales down a photo of MaxPixels pixels, which
// draw.CatmullRom.Scale alone needs over 300 MB of buffer for.
func TestScaleDownLargeImage(t *testing.T) {
	// Not parallel, so that the measured allocations are only this test's.
	const w, h = 8000, 5000
	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	// The left half is red and the right half blue.
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 255, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 255, A: 255}
			}
			yy, cb, cr := color.RGBToYCbCr(c.R, c.G, c.B)
			img.Y[img.YOffset(x, y)] = yy
			img.Cb[img.COffset(x, y)] = cb
			img.Cr[img.COffset(x, y)] = cr
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got := ScaleDown(img, 2048)
	runtime.ReadMemStats(&after)
	if b := got.Bounds(); b.Dx() != 2048 || b.Dy() != 1280 {
		t.Fatalf("ScaleDown is %dx%d, want 2048x1280", b.Dx(), b.Dy())
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; !raceEnabled && alloc > 150<<20 {
		t.Errorf("ScaleDown allocated %d MB, want at most 150 MB", alloc>>20)
	}
	// Every band of rows and columns is scaled.
	for _, p := range []image.Point{{0, 0}, {500, 640}, {1000, 1279}, {1100, 0}, {1600, 640}, {2047, 1279}} {
		r, g, b, _ := got.At(p.X, p.Y).RGBA()
		wantR, wantB := r > 0xe000, b < 0x2000
		if p.X > 1024 {
			wantR, wantB = r < 0x2000, b > 0xe000
		}
		if !wantR || !wantB || g > 0x2000 {
			t.Errorf("pixel %v is %v, want %s", p, got.At(p.X, p.Y), map[bool]string{true: "red", false: "blue"}[p.X < 1024])
		}
	}
}

// TestCatmullRomBands checks that scaling in bands matches scaling at once.
func TestCatmullRomBands(t *testing.T) {
	t.Parallel()
	// Large enough for several bands of rows and of columns, with smooth
	// gradients and a sharp edge.
	src := image.NewNRGBA(image.Rect(0, 0, 1500, 1200))
	for y := range 1200 {
		for x := range 1500 {
			v := uint8(128 + 100*math.Sin(float64(x)/40)*math.Cos(float64(y)/60))
			if x > 700 && x < 800 {
				v = 0
			}
			src.Set(x, y, color.NRGBA{R: v, G: uint8(x * 255 / 1500), B: uint8(y * 255 / 1200), A: uint8(155 + y%100)})
		}
	}
	dr := image.Rect(0, 0, 1000, 800)
	want := image.NewRGBA(dr)
	draw.Draw(want, dr, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(want, dr, src, src.Bounds(), draw.Over, nil)
	got := image.NewRGBA(dr)
	draw.Draw(got, dr, image.NewUniform(color.White), image.Point{}, draw.Src)
	catmullRom(got, src, src.Bounds())
	maxDiff := 0
	for i := range got.Pix {
		maxDiff = max(maxDiff, int(got.Pix[i])-int(want.Pix[i]), int(want.Pix[i])-int(got.Pix[i]))
	}
	if maxDiff > 3 {
		t.Errorf("scaling in bands differs from scaling at once by up to %d", maxDiff)
	}
}

// pngHeader returns the start of a PNG of w by h pixels with the bit depth
// and color type, enough for image.DecodeConfig.
func pngHeader(w, h int, depth, colorType byte) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = depth, colorType
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestDecodeLimitsMemory(t *testing.T) {
	t.Parallel()
	// 30 megapixels of 16 bit RGBA would decode to 240 MB.
	err := Decode(t.Context(), pngHeader(6000, 5000, 16, 6), ignore)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("Decode of a 30 megapixel 16 bit PNG returned %v, want it to be too large", err)
	}
	// 30 megapixels of 8 bit RGBA is allowed, but this PNG has no data.
	if err := Decode(t.Context(), pngHeader(6000, 5000, 8, 6), ignore); err != ErrNotImage {
		t.Errorf("Decode of a 30 megapixel 8 bit PNG without data returned %v, want ErrNotImage", err)
	}
}

// ignore is a use function for Decode that does nothing.
func ignore(image.Image) error { return nil }

// jpegHeader returns the start of a JPEG of w by h pixels with the frame
// header marker (0xc0 for baseline, 0xc2 for progressive) and number of
// components, enough for image.DecodeConfig.
func jpegHeader(w, h int, marker byte, components int) []byte {
	b := []byte{0xff, 0xd8}
	// A JFIF APP0 segment, without which image.DecodeConfig reads past the
	// frame header to find the color transform.
	b = append(b, 0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)
	sof := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), byte(components)}
	for i := range components {
		sof = append(sof, byte(i+1), 0x11, 0)
	}
	b = append(b, 0xff, marker, byte((len(sof)+2)>>8), byte(len(sof)+2))
	return append(b, sof...)
}

func TestDecodeLimitsJPEGMemory(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		w, h       int
		marker     byte
		components int
		tooLarge   bool
	}{
		// 3 bytes per pixel.
		{"baseline YCbCr", 6000, 5000, 0xc0, 3, false},
		// 3 bytes per pixel and 12 bytes of coefficients, 180 MB.
		{"progressive YCbCr", 4000, 3000, 0xc2, 3, true},
		{"small progressive YCbCr", 3000, 2000, 0xc2, 3, false},
		// 8 bytes per pixel, 200 MB.
		{"baseline CMYK", 5000, 5000, 0xc0, 4, true},
		{"small baseline CMYK", 4000, 4000, 0xc0, 4, false},
		// 1 byte per pixel and 4 bytes of coefficients.
		{"progressive gray", 6000, 5000, 0xc2, 1, false},
		{"large progressive gray", 8000, 5000, 0xc2, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Decode(t.Context(), jpegHeader(tt.w, tt.h, tt.marker, tt.components), ignore)
			if tt.tooLarge {
				if err == nil || !strings.Contains(err.Error(), "too large") {
					t.Errorf("Decode returned %v, want it to be too large", err)
				}
				return
			}
			// The header is allowed, but there is no data.
			if err != ErrNotImage {
				t.Errorf("Decode returned %v, want ErrNotImage", err)
			}
		})
	}
}

func TestDecodeOneAtATime(t *testing.T) {
	// Not parallel, as it holds the only decode slot.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	using := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error)
	go func() {
		done <- Decode(t.Context(), buf.Bytes(), func(image.Image) error {
			close(using)
			<-release
			return nil
		})
	}()
	<-using
	// Another image waits while the first is used, until its context is
	// done.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := Decode(ctx, buf.Bytes(), ignore); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Decode while another image is used returned %v, want context.DeadlineExceeded", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// Once the first is done, it can be decoded.
	if err := Decode(t.Context(), buf.Bytes(), ignore); err != nil {
		t.Errorf("Decode after the first image returned %v", err)
	}
}
