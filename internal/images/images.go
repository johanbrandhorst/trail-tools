// Package images decodes uploaded images and encodes the stored JPEGs.
package images

import (
	"bytes"
	"context"
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

// maxDecodeBytes limits the memory that decoding an image takes, so that
// images that take more bytes per pixel to decode, like 16 bit PNGs and
// progressive JPEGs, are limited to fewer pixels.
const maxDecodeBytes = MaxPixels * 4

// maxScaleBufferBytes limits the buffer of each draw.CatmullRom.Scale, see
// catmullRom.
const maxScaleBufferBytes = 8 << 20

// maxConcurrentDecodes limits the images decoded and used at once. Each can
// take up to maxDecodeBytes, and more to scale, and the server has one CPU,
// so decoding several at once wouldn't be faster anyway.
const maxConcurrentDecodes = 1

// decodeSlots holds a value for each image being decoded or used.
var decodeSlots = make(chan struct{}, maxConcurrentDecodes)

// ErrNotImage is returned for uploads that aren't a supported image.
var ErrNotImage = errors.New("the file is not a JPEG, PNG or WebP image")

// Decode decodes a JPEG, PNG or WebP image of at most MaxPixels pixels, that
// takes at most maxDecodeBytes to decode, and calls use with it. At most
// maxConcurrentDecodes images are decoded and used at once; Decode waits for
// its turn until ctx is done.
func Decode(ctx context.Context, data []byte, use func(image.Image) error) error {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ErrNotImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return ErrNotImage
	}
	perPixel := decodeBytesPerPixel(data, cfg.ColorModel, format)
	if pixels := cfg.Width * cfg.Height; pixels > MaxPixels || pixels*perPixel > maxDecodeBytes {
		return fmt.Errorf("the image is too large: %dx%d pixels, the maximum for this image is %d megapixels; scale it down and try again",
			cfg.Width, cfg.Height, min(MaxPixels, maxDecodeBytes/perPixel)/1_000_000)
	}
	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ErrNotImage
	}
	return use(img)
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

// decodeBytesPerPixel returns the bytes per pixel that decoding an image of
// the format and color model takes, at most.
func decodeBytesPerPixel(data []byte, m color.Model, format string) int {
	n := bytesPerPixel(m)
	switch format {
	case "jpeg":
		components := 3
		switch m {
		case color.GrayModel:
			components = 1
		case color.CMYKModel:
			// CMYK images are decoded to YCbCr and a black plane, and then
			// converted.
			components, n = 4, 3+1+4
		case color.RGBAModel:
			// RGB images are decoded to YCbCr and then converted.
			n = 3 + 4
		}
		// Progressive images keep the coefficients of every block, 4 bytes
		// per pixel and component, until the last scan.
		if progressiveJPEG(data) {
			n += 4 * components
		}
	case "png":
		// Interlaced images are decoded a pass at a time, into images
		// that add up to the size of the whole, before being merged.
		if len(data) > 28 && data[28] == 1 {
			n *= 2
		}
	}
	return n
}

// progressiveJPEG reports whether a JPEG is progressive: whether its frame
// header, the first segment that starts with a SOFn marker, is SOF2.
func progressiveJPEG(data []byte) bool {
	// Skip the SOI marker, then each segment: a marker and a 2 byte length
	// that includes itself.
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return false
		}
		marker := data[i+1]
		switch {
		case marker == 0xff:
			// Fill bytes may precede a marker.
			i++
			continue
		case marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc:
			return marker == 0xc2
		}
		i += 2 + (int(data[i+2])<<8 | int(data[i+3]))
	}
	return false
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
