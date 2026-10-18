package images

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"regexp"
	"time"
)

// Metadata is what a photo's EXIF metadata says about how to show it, and
// where and when it was taken. Browsers read it before uploading, but other
// clients, like AI agents, upload photos as they are.
type Metadata struct {
	// Orientation is the EXIF orientation, from 1 to 8: how the stored pixels
	// are rotated and flipped from the upright photo. 1 if they're upright or
	// it's unknown.
	Orientation int
	// The GPS position, if HasLocation.
	Lat, Lng    float64
	HasLocation bool
	// When the photo was taken, if its UTC offset was recorded too. Zero
	// otherwise.
	TakenTime time.Time
	// When the photo was taken as a local time without an offset
	// (YYYY-MM-DDTHH:MM:SS), if the offset wasn't recorded, as by most
	// cameras. Empty otherwise.
	TakenLocalTime string
}

// The EXIF tags read, in IFD0, the Exif IFD and the GPS IFD.
const (
	tagOrientation         = 0x0112
	tagExifIFD             = 0x8769
	tagGPSIFD              = 0x8825
	tagDateTimeOriginal    = 0x9003
	tagDateTimeDigitized   = 0x9004
	tagOffsetTimeOriginal  = 0x9011
	tagOffsetTimeDigitized = 0x9012
	tagGPSLatitudeRef      = 0x0001
	tagGPSLatitude         = 0x0002
	tagGPSLongitudeRef     = 0x0003
	tagGPSLongitude        = 0x0004
)

// The EXIF value types read.
const (
	typeASCII    = 2
	typeShort    = 3
	typeLong     = 4
	typeRational = 5
)

var (
	// exifDate matches EXIF dates, like "2026:09:07 10:15:30".
	exifDate = regexp.MustCompile(`^(\d{4}):(\d{2}):(\d{2})[ T](\d{2}):(\d{2}):(\d{2})`)
	// exifOffset matches EXIF UTC offsets, like "+02:00".
	exifOffset = regexp.MustCompile(`^[+-]\d{2}:\d{2}$`)
	// exifHeader starts the EXIF data in JPEG APP1 segments, and sometimes in
	// WebP EXIF chunks.
	exifHeader = []byte("Exif\x00\x00")
)

// ReadMetadata reads the EXIF metadata of a JPEG, PNG or WebP image. Missing
// or malformed metadata only leaves out what can't be read, as it only means
// the photo isn't placed or rotated automatically.
func ReadMetadata(data []byte) Metadata {
	md := Metadata{Orientation: 1}
	tiff := findEXIF(data)
	if tiff == nil {
		return md
	}
	order, ifd0, ok := tiffHeader(tiff)
	if !ok {
		return md
	}
	r := exifReader{data: tiff, order: order}
	tags := r.ifd(ifd0)
	if o, ok := r.uint(tags[tagOrientation]); ok && o >= 1 && o <= 8 {
		md.Orientation = int(o)
	}
	if offset, ok := r.uint(tags[tagExifIFD]); ok {
		exif := r.ifd(offset)
		md.TakenTime, md.TakenLocalTime = r.takenTime(exif[tagDateTimeOriginal], exif[tagOffsetTimeOriginal])
		if md.TakenTime.IsZero() && md.TakenLocalTime == "" {
			md.TakenTime, md.TakenLocalTime = r.takenTime(exif[tagDateTimeDigitized], exif[tagOffsetTimeDigitized])
		}
	}
	if offset, ok := r.uint(tags[tagGPSIFD]); ok {
		gps := r.ifd(offset)
		lat, latOK := r.coordinate(gps[tagGPSLatitude], gps[tagGPSLatitudeRef], "N", "S")
		lng, lngOK := r.coordinate(gps[tagGPSLongitude], gps[tagGPSLongitudeRef], "E", "W")
		// Cameras without a fix write 0, 0, which is in the ocean.
		if latOK && lngOK && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && (lat != 0 || lng != 0) {
			md.Lat, md.Lng, md.HasLocation = lat, lng, true
		}
	}
	return md
}

// findEXIF returns the TIFF structure of the EXIF metadata of a JPEG, PNG or
// WebP image, or nil if it has none.
func findEXIF(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8}):
		return jpegEXIF(data)
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return pngEXIF(data)
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return webpEXIF(data)
	}
	return nil
}

// jpegEXIF returns the EXIF metadata of a JPEG, from its APP1 segment, which
// comes before the image data.
func jpegEXIF(data []byte) []byte {
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return nil
		}
		marker := data[i+1]
		switch {
		case marker == 0xff:
			// Fill bytes may pad markers.
			i++
			continue
		case marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			// Markers without a length.
			i += 2
			continue
		case marker == 0xd9 || marker == 0xda:
			// The end of the image, or the start of its data.
			return nil
		}
		length := int(binary.BigEndian.Uint16(data[i+2:]))
		if length < 2 || i+2+length > len(data) {
			return nil
		}
		segment := data[i+4 : i+2+length]
		if marker == 0xe1 && bytes.HasPrefix(segment, exifHeader) {
			return segment[len(exifHeader):]
		}
		i += 2 + length
	}
	return nil
}

// pngEXIF returns the EXIF metadata of a PNG, from its eXIf chunk.
func pngEXIF(data []byte) []byte {
	for i := 8; i+8 <= len(data); {
		length := binary.BigEndian.Uint32(data[i:])
		kind := string(data[i+4 : i+8])
		if uint64(length) > uint64(len(data)-i-8) {
			return nil
		}
		if kind == "eXIf" {
			return data[i+8 : i+8+int(length)]
		}
		if kind == "IEND" {
			return nil
		}
		// The chunk's type, data and CRC.
		i += 8 + int(length) + 4
	}
	return nil
}

// webpEXIF returns the EXIF metadata of a WebP, from its EXIF chunk.
func webpEXIF(data []byte) []byte {
	for i := 12; i+8 <= len(data); {
		kind := string(data[i : i+4])
		size := binary.LittleEndian.Uint32(data[i+4:])
		if uint64(size) > uint64(len(data)-i-8) {
			return nil
		}
		if kind == "EXIF" {
			chunk := data[i+8 : i+8+int(size)]
			// Some encoders include the header JPEGs have.
			return bytes.TrimPrefix(chunk, exifHeader)
		}
		// Chunks are padded to an even size.
		i += 8 + int(size) + int(size%2)
	}
	return nil
}

// tiffHeader returns the byte order of EXIF metadata, and the offset of its
// first IFD.
func tiffHeader(tiff []byte) (binary.ByteOrder, uint32, bool) {
	if len(tiff) < 8 {
		return nil, 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, 0, false
	}
	if order.Uint16(tiff[2:]) != 42 {
		return nil, 0, false
	}
	return order, order.Uint32(tiff[4:]), true
}

// exifEntry is an entry of an IFD.
type exifEntry struct {
	kind  uint16
	count uint32
	// The value if it fits in 4 bytes, or else its offset.
	value []byte
}

// exifReader reads the IFDs of EXIF metadata. Every offset is checked
// against the data, as uploads can't be trusted.
type exifReader struct {
	data  []byte
	order binary.ByteOrder
}

// ifd returns the entries of the IFD at the offset by tag, or none if it's
// out of bounds.
func (r exifReader) ifd(offset uint32) map[uint16]exifEntry {
	entries := map[uint16]exifEntry{}
	if uint64(offset)+2 > uint64(len(r.data)) {
		return entries
	}
	count := int(r.order.Uint16(r.data[offset:]))
	start := int(offset) + 2
	for i := range count {
		e := start + 12*i
		if e+12 > len(r.data) {
			break
		}
		entries[r.order.Uint16(r.data[e:])] = exifEntry{
			kind:  r.order.Uint16(r.data[e+2:]),
			count: r.order.Uint32(r.data[e+4:]),
			value: r.data[e+8 : e+12],
		}
	}
	return entries
}

// bytes returns the bytes of an entry's value of size bytes per item, or
// false if it's out of bounds.
func (r exifReader) bytes(e exifEntry, size int) ([]byte, bool) {
	n := uint64(e.count) * uint64(size)
	if n <= 4 {
		return e.value[:n], true
	}
	offset := uint64(r.order.Uint32(e.value))
	if offset+n > uint64(len(r.data)) {
		return nil, false
	}
	return r.data[offset : offset+n], true
}

// uint returns the value of a SHORT or LONG entry.
func (r exifReader) uint(e exifEntry) (uint32, bool) {
	switch {
	case e.count != 1:
		return 0, false
	case e.kind == typeShort:
		return uint32(r.order.Uint16(e.value)), true
	case e.kind == typeLong:
		return r.order.Uint32(e.value), true
	}
	return 0, false
}

// string returns the value of an ASCII entry, without its NUL terminator.
func (r exifReader) string(e exifEntry) string {
	if e.kind != typeASCII {
		return ""
	}
	b, ok := r.bytes(e, 1)
	if !ok {
		return ""
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// takenTime returns when a photo was taken from a date entry and its UTC
// offset entry: as a time if the offset is known, or else as a local time.
func (r exifReader) takenTime(date, offset exifEntry) (time.Time, string) {
	m := exifDate.FindStringSubmatch(r.string(date))
	if m == nil {
		return time.Time{}, ""
	}
	local := m[1] + "-" + m[2] + "-" + m[3] + "T" + m[4] + ":" + m[5] + ":" + m[6]
	if off := r.string(offset); exifOffset.MatchString(off) {
		if t, err := time.Parse("2006-01-02T15:04:05-07:00", local+off); err == nil {
			return t, ""
		}
	}
	if _, err := time.Parse("2006-01-02T15:04:05", local); err != nil {
		// Unset dates are often all zeros or spaces.
		return time.Time{}, ""
	}
	return time.Time{}, local
}

// coordinate returns a GPS latitude or longitude in degrees from its entry
// of three rationals, degrees, minutes and seconds, and its reference entry,
// which is neg for southern and western coordinates.
func (r exifReader) coordinate(value, ref exifEntry, pos, neg string) (float64, bool) {
	if value.kind != typeRational || value.count != 3 {
		return 0, false
	}
	b, ok := r.bytes(value, 8)
	if !ok {
		return 0, false
	}
	var parts [3]float64
	for i := range parts {
		num, den := r.order.Uint32(b[8*i:]), r.order.Uint32(b[8*i+4:])
		if den == 0 {
			return 0, false
		}
		parts[i] = float64(num) / float64(den)
	}
	degrees := parts[0] + parts[1]/60 + parts[2]/3600
	switch r.string(ref) {
	case pos:
	case neg:
		degrees = -degrees
	default:
		return 0, false
	}
	if math.IsNaN(degrees) || math.IsInf(degrees, 0) {
		return 0, false
	}
	return degrees, true
}

// Orient returns the image rotated and flipped upright from its EXIF
// orientation. Orientations other than 2 to 8 return the image as it is.
func Orient(img image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return img
	}
	src, ok := img.(*image.RGBA)
	if !ok {
		src = toRGBA(img)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		// Orientations 5 to 8 turn the photo by 90 degrees.
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			// The source pixel shown at x, y.
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			si := src.PixOffset(b.Min.X+sx, b.Min.Y+sy)
			copy(dst.Pix[dst.PixOffset(x, y):][:4], src.Pix[si:si+4])
		}
	}
	return dst
}
