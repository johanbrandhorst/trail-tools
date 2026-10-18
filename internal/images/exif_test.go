package images

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"
)

// testTag is an IFD entry of test EXIF metadata.
type testTag struct {
	tag, kind uint16
	count     uint32
	data      []byte
}

// tiffBuilder builds the TIFF structure of EXIF metadata in a byte order.
type tiffBuilder struct {
	order binary.ByteOrder
}

func (b tiffBuilder) ascii(tag uint16, s string) testTag {
	data := append([]byte(s), 0)
	return testTag{tag: tag, kind: typeASCII, count: uint32(len(data)), data: data}
}

func (b tiffBuilder) short(tag, v uint16) testTag {
	data := make([]byte, 2)
	b.order.PutUint16(data, v)
	return testTag{tag: tag, kind: typeShort, count: 1, data: data}
}

func (b tiffBuilder) long(tag uint16, v uint32) testTag {
	data := make([]byte, 4)
	b.order.PutUint32(data, v)
	return testTag{tag: tag, kind: typeLong, count: 1, data: data}
}

// degrees returns a GPS coordinate as degrees, minutes and seconds.
func (b tiffBuilder) degrees(tag uint16, d, m, sHundredths uint32) testTag {
	data := make([]byte, 24)
	for i, v := range [][2]uint32{{d, 1}, {m, 1}, {sHundredths, 100}} {
		b.order.PutUint32(data[8*i:], v[0])
		b.order.PutUint32(data[8*i+4:], v[1])
	}
	return testTag{tag: tag, kind: typeRational, count: 3, data: data}
}

// build returns the TIFF structure with the tags of IFD0, the Exif IFD and
// the GPS IFD, leaving out empty sub-IFDs.
func (b tiffBuilder) build(ifd0, exif, gps []testTag) []byte {
	ifdSize := func(tags []testTag) int { return 2 + 12*len(tags) + 4 }
	n0 := len(ifd0)
	if exif != nil {
		n0++
	}
	if gps != nil {
		n0++
	}
	exifOffset := 8 + 2 + 12*n0 + 4
	gpsOffset := exifOffset
	if exif != nil {
		gpsOffset += ifdSize(exif)
	}
	dataOffset := gpsOffset
	if gps != nil {
		dataOffset += ifdSize(gps)
	}
	tags0 := append([]testTag(nil), ifd0...)
	if exif != nil {
		tags0 = append(tags0, b.long(tagExifIFD, uint32(exifOffset)))
	}
	if gps != nil {
		tags0 = append(tags0, b.long(tagGPSIFD, uint32(gpsOffset)))
	}
	out := make([]byte, dataOffset)
	if b.order == binary.LittleEndian {
		copy(out, "II")
	} else {
		copy(out, "MM")
	}
	b.order.PutUint16(out[2:], 42)
	b.order.PutUint32(out[4:], 8)
	var data []byte
	writeIFD := func(at int, tags []testTag) {
		b.order.PutUint16(out[at:], uint16(len(tags)))
		for i, t := range tags {
			e := at + 2 + 12*i
			b.order.PutUint16(out[e:], t.tag)
			b.order.PutUint16(out[e+2:], t.kind)
			b.order.PutUint32(out[e+4:], t.count)
			if len(t.data) <= 4 {
				copy(out[e+8:], t.data)
			} else {
				b.order.PutUint32(out[e+8:], uint32(dataOffset+len(data)))
				data = append(data, t.data...)
			}
		}
	}
	writeIFD(8, tags0)
	if exif != nil {
		writeIFD(exifOffset, exif)
	}
	if gps != nil {
		writeIFD(gpsOffset, gps)
	}
	return append(out, data...)
}

// testImage returns a small image to encode.
func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := range 3 {
		for x := range 4 {
			img.Set(x, y, color.RGBA{R: uint8(60 * x), G: uint8(80 * y), A: 255})
		}
	}
	return img
}

// jpegWithEXIF returns a JPEG with the EXIF metadata in an APP1 segment
// after a JFIF APP0 segment, as cameras write them.
func jpegWithEXIF(t *testing.T, tiff []byte) []byte {
	t.Helper()
	encoded := plainJPEG(t)
	app0 := []byte{0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	segment := append(append([]byte{}, exifHeader...), tiff...)
	app1 := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(segment)+2))
	out := append([]byte{}, encoded[:2]...)
	out = append(out, app0...)
	out = append(out, app1...)
	out = append(out, segment...)
	return append(out, encoded[2:]...)
}

// plainJPEG returns a JPEG without metadata, as browsers upload them.
func plainJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(), nil); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

// pngWithEXIF returns a PNG with the EXIF metadata in an eXIf chunk after
// its IHDR chunk.
func pngWithEXIF(t *testing.T, tiff []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage()); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	encoded := buf.Bytes()
	// The signature and the IHDR chunk.
	const headerLength = 8 + 25
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(tiff)))
	chunk = append(chunk, "eXIf"...)
	chunk = append(chunk, tiff...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	out := append([]byte{}, encoded[:headerLength]...)
	out = append(out, chunk...)
	return append(out, encoded[headerLength:]...)
}

// webpWithEXIF returns a WebP container with a VP8X chunk and the EXIF
// metadata in an EXIF chunk, but no image, which ReadMetadata doesn't need.
func webpWithEXIF(exif []byte) []byte {
	var body []byte
	body = append(body, "WEBP"...)
	body = append(body, "VP8X"...)
	body = binary.LittleEndian.AppendUint32(body, 10)
	body = append(body, make([]byte, 10)...)
	body = append(body, "EXIF"...)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(exif)))
	body = append(body, exif...)
	if len(exif)%2 == 1 {
		body = append(body, 0)
	}
	out := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...)
	return append(out, body...)
}

func TestReadMetadata(t *testing.T) {
	t.Parallel()
	le := tiffBuilder{order: binary.LittleEndian}
	be := tiffBuilder{order: binary.BigEndian}
	// A phone photo: rotated, with its UTC offset, near Zermatt.
	phone := le.build(
		[]testTag{le.ascii(0x010f, "Apple"), le.short(tagOrientation, 6)},
		[]testTag{
			le.ascii(tagDateTimeOriginal, "2026:09:07 10:15:30"),
			le.ascii(tagOffsetTimeOriginal, "+02:00"),
		},
		[]testTag{
			le.ascii(tagGPSLatitudeRef, "N"),
			le.degrees(tagGPSLatitude, 45, 58, 3000),
			le.ascii(tagGPSLongitudeRef, "E"),
			le.degrees(tagGPSLongitude, 7, 39, 4500),
		},
	)
	phoneWant := Metadata{
		Orientation: 6,
		Lat:         45 + 58.0/60 + 30.0/3600,
		Lng:         7 + 39.0/60 + 45.0/3600,
		HasLocation: true,
		TakenTime:   time.Date(2026, 9, 7, 8, 15, 30, 0, time.UTC),
	}
	// A camera's photo: big-endian, local time only, in the southern and
	// western hemispheres.
	camera := be.build(
		nil,
		[]testTag{be.ascii(tagDateTimeOriginal, "2026:03:14 07:05:09")},
		[]testTag{
			be.ascii(tagGPSLatitudeRef, "S"),
			be.degrees(tagGPSLatitude, 33, 30, 0),
			be.ascii(tagGPSLongitudeRef, "W"),
			be.degrees(tagGPSLongitude, 70, 15, 0),
		},
	)
	cameraWant := Metadata{
		Orientation:    1,
		Lat:            -33.5,
		Lng:            -70.25,
		HasLocation:    true,
		TakenLocalTime: "2026-03-14T07:05:09",
	}
	for _, tt := range []struct {
		name string
		data []byte
		want Metadata
	}{
		{"jpeg", jpegWithEXIF(t, phone), phoneWant},
		{"big-endian jpeg", jpegWithEXIF(t, camera), cameraWant},
		{"png", pngWithEXIF(t, phone), phoneWant},
		{"webp", webpWithEXIF(phone), phoneWant},
		{"webp with header", webpWithEXIF(append(append([]byte{}, exifHeader...), phone...)), phoneWant},
		{
			"digitized time",
			jpegWithEXIF(t, le.build(nil, []testTag{
				le.ascii(tagDateTimeDigitized, "2026:09:07 10:15:30"),
				le.ascii(tagOffsetTimeDigitized, "-07:00"),
			}, nil)),
			Metadata{Orientation: 1, TakenTime: time.Date(2026, 9, 7, 17, 15, 30, 0, time.UTC)},
		},
		{
			"unset values",
			jpegWithEXIF(t, le.build(
				[]testTag{le.short(tagOrientation, 9)},
				[]testTag{le.ascii(tagDateTimeOriginal, "0000:00:00 00:00:00")},
				[]testTag{
					le.ascii(tagGPSLatitudeRef, "N"),
					le.degrees(tagGPSLatitude, 0, 0, 0),
					le.ascii(tagGPSLongitudeRef, "E"),
					le.degrees(tagGPSLongitude, 0, 0, 0),
				},
			)),
			Metadata{Orientation: 1},
		},
		{"no metadata", plainJPEG(t), Metadata{Orientation: 1}},
		{"not an image", []byte("hello"), Metadata{Orientation: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ReadMetadata(tt.data)
			if got.Orientation != tt.want.Orientation ||
				got.HasLocation != tt.want.HasLocation ||
				!near(got.Lat, tt.want.Lat) || !near(got.Lng, tt.want.Lng) ||
				!got.TakenTime.Equal(tt.want.TakenTime) ||
				got.TakenLocalTime != tt.want.TakenLocalTime {
				t.Errorf("ReadMetadata = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func near(a, b float64) bool {
	return a-b < 1e-9 && b-a < 1e-9
}

// TestReadMetadataMalformed reads metadata cut short and corrupted
// everywhere, which must not panic.
func TestReadMetadataMalformed(t *testing.T) {
	t.Parallel()
	b := tiffBuilder{order: binary.LittleEndian}
	data := jpegWithEXIF(t, b.build(
		[]testTag{b.short(tagOrientation, 6)},
		[]testTag{b.ascii(tagDateTimeOriginal, "2026:09:07 10:15:30")},
		[]testTag{b.ascii(tagGPSLatitudeRef, "N"), b.degrees(tagGPSLatitude, 45, 58, 3000)},
	))
	for n := range len(data) {
		ReadMetadata(data[:n])
	}
	for i := range len(data) {
		corrupted := append([]byte{}, data...)
		corrupted[i] ^= 0xff
		ReadMetadata(corrupted)
	}
}

func FuzzReadMetadata(f *testing.F) {
	b := tiffBuilder{order: binary.BigEndian}
	tiff := b.build(
		[]testTag{b.short(tagOrientation, 3)},
		[]testTag{b.ascii(tagDateTimeOriginal, "2026:09:07 10:15:30"), b.ascii(tagOffsetTimeOriginal, "+02:00")},
		[]testTag{b.ascii(tagGPSLongitudeRef, "W"), b.degrees(tagGPSLongitude, 7, 39, 4500)},
	)
	f.Add(append(append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(tiff) + 8)}, exifHeader...), tiff...))
	f.Add(webpWithEXIF(tiff))
	f.Fuzz(func(_ *testing.T, data []byte) {
		ReadMetadata(data)
	})
}

func TestOrient(t *testing.T) {
	t.Parallel()
	// The stored image is 3 by 2 pixels, with its first pixel red and the
	// second green. The EXIF specification says where the first row and
	// column of the stored image are shown, and so where those pixels go.
	const w, h = 3, 2
	stored := image.NewRGBA(image.Rect(0, 0, w, h))
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	stored.Set(0, 0, red)
	stored.Set(1, 0, green)
	type point struct{ x, y int }
	for _, tt := range []struct {
		orientation int
		// Where the first and second pixels of the first row are shown.
		first, second point
	}{
		{1, point{0, 0}, point{1, 0}},
		{2, point{2, 0}, point{1, 0}},
		{3, point{2, 1}, point{1, 1}},
		{4, point{0, 1}, point{1, 1}},
		// The first row is shown on the left, top to bottom.
		{5, point{0, 0}, point{0, 1}},
		// The first row is shown on the right, top to bottom.
		{6, point{1, 0}, point{1, 1}},
		// The first row is shown on the right, bottom to top.
		{7, point{1, 2}, point{1, 1}},
		// The first row is shown on the left, bottom to top.
		{8, point{0, 2}, point{0, 1}},
	} {
		got := Orient(stored, tt.orientation)
		wantW, wantH := w, h
		if tt.orientation >= 5 {
			wantW, wantH = h, w
		}
		if b := got.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
			t.Errorf("Orient(%d) is %dx%d, want %dx%d", tt.orientation, b.Dx(), b.Dy(), wantW, wantH)
			continue
		}
		if c := got.At(tt.first.x, tt.first.y); c != red {
			t.Errorf("Orient(%d) at %v is %v, want red", tt.orientation, tt.first, c)
		}
		if c := got.At(tt.second.x, tt.second.y); c != green {
			t.Errorf("Orient(%d) at %v is %v, want green", tt.orientation, tt.second, c)
		}
	}
}

func TestDecodeHEIF(t *testing.T) {
	t.Parallel()
	heic := append([]byte{0, 0, 0, 24}, "ftypheic\x00\x00\x00\x00mif1heic"...)
	if err := Decode(t.Context(), heic, func(image.Image) error { return nil }); err != ErrHEIF {
		t.Errorf("Decode returned %v, want ErrHEIF", err)
	}
}
