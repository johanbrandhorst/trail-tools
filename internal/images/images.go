// Package images decodes uploaded images and encodes the stored JPEGs.
package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"

	// Register the decoders of the accepted formats.
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxPixels limits the size of decoded images, to bound memory.
const MaxPixels = 40_000_000

// maxDecodedBytes limits the memory of decoded images, so that images with
// more bytes per pixel, like 16 bit PNGs, are limited to fewer pixels.
const maxDecodedBytes = MaxPixels * 4

// maxScaleBufferBytes limits the buffer of each draw.CatmullRom.Scale, see
// catmullRom.
const maxScaleBufferBytes = 8 << 20

var (
	// ErrNotImage is returned for uploads that aren't a supported image.
	ErrNotImage = errors.New("the file is not a JPEG, PNG or WebP image")
	// ErrHEIF is returned for HEIC and AVIF images, as phones take them, which
	// can't be decoded. Browsers that can show them convert them before
	// uploading, but other clients upload them as they are.
	ErrHEIF = errors.New("the image is HEIC or AVIF, which isn't supported; convert it to JPEG first, keeping its metadata")
)

// Decode decodes a JPEG, PNG or WebP image of at most MaxPixels pixels.
func Decode(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if isHEIF(data) {
			return nil, ErrHEIF
		}
		return nil, ErrNotImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrNotImage
	}
	if cfg.Width*cfg.Height > MaxPixels || cfg.Width*cfg.Height*bytesPerPixel(cfg.ColorModel) > maxDecodedBytes {
		return nil, fmt.Errorf("the image is too large: %dx%d pixels, the maximum is %d megapixels", cfg.Width, cfg.Height, MaxPixels/1_000_000)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrNotImage
	}
	return img, nil
}

// isHEIF reports whether data is a HEIF image, like HEIC or AVIF, from the
// brand of its ftyp box.
func isHEIF(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	switch string(data[8:12]) {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1", "avif", "avis":
		return true
	}
	return false
}

// ScaleDown returns the image scaled to fit within size by size pixels, on
// a white background so that transparent areas aren't black in a JPEG.
func ScaleDown(img image.Image, size int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > size || h > size {
		if w >= h {
			w, h = size, max(1, h*size/w)
		} else {
			w, h = max(1, w*size/h), size
		}
	}
	return scale(img, b, w, h)
}

// Square returns the middle square of the image, scaled down to at most
// size by size pixels, on a white background.
func Square(img image.Image, size int) image.Image {
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x, y := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	crop := image.Rect(x, y, x+side, y+side)
	out := min(side, size)
	return scale(img, crop, out, out)
}

// scale draws the src rectangle of the image scaled to w by h pixels, on a
// white background.
func scale(img image.Image, src image.Rectangle, w, h int) image.Image {
	img, src = halve(img, src, w, h)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if w == src.Dx() && h == src.Dy() {
		draw.Draw(dst, dst.Bounds(), img, src.Min, draw.Over)
	} else {
		catmullRom(dst, img, src)
	}
	return dst
}

// catmullRom draws the src rectangle of the image scaled to dst's bounds
// over dst. draw.CatmullRom.Scale needs a buffer of 32 bytes for each
// destination column of each source row, hundreds of megabytes for large
// photos, so this scales the rows and then the columns, each in bands
// small enough to bound the buffer. Scaling only rows or columns doesn't mix
// neighboring bands, so they have no seams.
func catmullRom(dst *image.RGBA, img image.Image, src image.Rectangle) {
	w, h, sw, sh := dst.Bounds().Dx(), dst.Bounds().Dy(), src.Dx(), src.Dy()
	rows := image.NewRGBA(image.Rect(0, 0, w, sh))
	// The scalers reuse their buffer for every band but a smaller last one.
	band := min(max(1, maxScaleBufferBytes/(32*w)), sh)
	scaler := draw.CatmullRom.NewScaler(w, band, sw, band)
	for y := 0; y < sh; y += band {
		n := min(band, sh-y)
		scaler.Scale(rows, image.Rect(0, y, w, y+n),
			img, image.Rect(src.Min.X, src.Min.Y+y, src.Max.X, src.Min.Y+y+n), draw.Src, nil)
	}
	band = min(max(1, maxScaleBufferBytes/(32*sh)), w)
	scaler = draw.CatmullRom.NewScaler(band, h, band, sh)
	for x := 0; x < w; x += band {
		n := min(band, w-x)
		scaler.Scale(dst, image.Rect(x, 0, x+n, h), rows, image.Rect(x, 0, x+n, sh), draw.Over, nil)
	}
}

// halve halves the src rectangle of the image while it's at least twice w
// by h pixels, returning the halved image and its bounds. Halving with
// draw.ApproxBiLinear averages each 2 by 2 pixels, roughly for odd sizes,
// which is cheap and keeps the quality of the final scale, while bounding
// its work and memory.
func halve(img image.Image, src image.Rectangle, w, h int) (image.Image, image.Rectangle) {
	for src.Dx() >= 2*w && src.Dy() >= 2*h {
		half := image.NewRGBA(image.Rect(0, 0, src.Dx()/2, src.Dy()/2))
		draw.ApproxBiLinear.Scale(half, half.Bounds(), img, src, draw.Src, nil)
		img, src = half, half.Bounds()
	}
	return img, src
}

// toRGBA returns a copy of the image as an RGBA image.
func toRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// bytesPerPixel returns the bytes per pixel of decoded images with the color
// model, at most.
func bytesPerPixel(m color.Model) int {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// EncodeJPEG encodes an image as a JPEG. Encoding drops the metadata of the
// original upload, such as its GPS position.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %w", err)
	}
	return buf.Bytes(), nil
}
