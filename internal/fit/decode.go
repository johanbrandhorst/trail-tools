package fit

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MaxFileSize is the largest FIT file Decode reads. Watch activities are
// rarely over a few megabytes; a day long ultra with second by second
// records is about 10.
const MaxFileSize = 32 << 20

// Message is a decoded FIT data message: its global number and the raw
// bytes of its fields, by field number, in the file's byte order.
type Message struct {
	Global uint16
	Fields map[uint8][]byte
	// The byte order of the fields.
	order binary.ByteOrder
	// The base types of the fields.
	types map[uint8]uint8
}

// Has reports whether the message has a valid value for a field: present,
// and not the base type's invalid value.
func (m Message) Has(num uint8) bool {
	_, ok := m.uint(num)
	return ok
}

// uint returns the first value of an integer field, and false if it's
// missing or invalid.
func (m Message) uint(num uint8) (uint64, bool) {
	b, ok := m.Fields[num]
	if !ok || len(b) == 0 {
		return 0, false
	}
	switch m.types[num] {
	case Enum, Uint8, 0x0A: // Enum, uint8 and uint8z.
		return uint64(b[0]), b[0] != 0xFF || m.types[num] == 0x0A && b[0] != 0
	case 0x01: // Sint8.
		return uint64(int64(int8(b[0]))), b[0] != 0x7F
	case 0x83: // Sint16.
		if len(b) < 2 {
			return 0, false
		}
		v := m.order.Uint16(b)
		return uint64(int64(int16(v))), v != 0x7FFF
	case Uint16, 0x8B: // Uint16 and uint16z.
		if len(b) < 2 {
			return 0, false
		}
		v := m.order.Uint16(b)
		return uint64(v), v != 0xFFFF && (m.types[num] != 0x8B || v != 0)
	case Sint32:
		if len(b) < 4 {
			return 0, false
		}
		v := m.order.Uint32(b)
		return uint64(int64(int32(v))), v != 0x7FFFFFFF
	case Uint32, 0x8C: // Uint32 and uint32z.
		if len(b) < 4 {
			return 0, false
		}
		v := m.order.Uint32(b)
		return uint64(v), v != 0xFFFFFFFF && (m.types[num] != 0x8C || v != 0)
	default:
		return 0, false
	}
}

// Uint returns an unsigned integer field, and false if it's missing or
// invalid.
func (m Message) Uint(num uint8) (uint64, bool) { return m.uint(num) }

// Int returns a signed integer field, and false if it's missing or invalid.
func (m Message) Int(num uint8) (int64, bool) {
	v, ok := m.uint(num)
	return int64(v), ok
}

// String returns a string field, empty if it's missing.
func (m Message) String(num uint8) string {
	b := m.Fields[num]
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// ErrNotFIT is returned for data that isn't a FIT file.
var ErrNotFIT = errors.New("not a FIT file")

// Decode decodes the data messages of a FIT file, checking its CRC. It
// handles compressed timestamp headers, filling in the timestamp field of
// their messages, and skips developer fields.
func Decode(file []byte) ([]Message, error) {
	if len(file) > MaxFileSize {
		return nil, fmt.Errorf("the file is %d bytes, more than %d", len(file), MaxFileSize)
	}
	if len(file) < 12 || string(file[8:12]) != ".FIT" {
		return nil, ErrNotFIT
	}
	headerSize := int(file[0])
	if headerSize < 12 || headerSize > len(file) {
		return nil, ErrNotFIT
	}
	size := int(binary.LittleEndian.Uint32(file[4:]))
	if headerSize+size+2 > len(file) {
		return nil, fmt.Errorf("the file is cut short: %d bytes of %d", len(file), headerSize+size+2)
	}
	if crc := binary.LittleEndian.Uint16(file[headerSize+size:]); crc != 0 && crc != CRC(0, file[:headerSize+size]) {
		return nil, errors.New("the file is corrupt: its CRC doesn't match")
	}
	data := file[headerSize : headerSize+size]

	type definition struct {
		global uint16
		order  binary.ByteOrder
		fields []Field
		// The total size of the developer fields, which are skipped.
		devSize int
	}
	var defs [16]*definition
	var messages []Message
	var lastTimestamp uint32
	need := func(n int) error {
		if len(data) < n {
			return errors.New("the file is cut short inside a message")
		}
		return nil
	}
	for len(data) > 0 {
		header := data[0]
		data = data[1:]
		compressed := header&0x80 != 0
		local := header & 0x0F
		if compressed {
			local = (header >> 5) & 0x03
		}
		if !compressed && header&0x40 != 0 {
			if err := need(5); err != nil {
				return nil, err
			}
			def := &definition{order: binary.LittleEndian}
			if data[1] == 1 {
				def.order = binary.BigEndian
			}
			def.global = def.order.Uint16(data[2:])
			n := int(data[4])
			data = data[5:]
			if err := need(3 * n); err != nil {
				return nil, err
			}
			for i := range n {
				def.fields = append(def.fields, Field{Num: data[3*i], Size: data[3*i+1], BaseType: data[3*i+2]})
			}
			data = data[3*n:]
			if header&0x20 != 0 {
				if err := need(1); err != nil {
					return nil, err
				}
				dev := int(data[0])
				data = data[1:]
				if err := need(3 * dev); err != nil {
					return nil, err
				}
				for i := range dev {
					def.devSize += int(data[3*i+1])
				}
				data = data[3*dev:]
			}
			defs[local] = def
			continue
		}
		def := defs[local]
		if def == nil {
			return nil, fmt.Errorf("a message of local type %d has no definition", local)
		}
		m := Message{Global: def.global, Fields: map[uint8][]byte{}, order: def.order, types: map[uint8]uint8{}}
		for _, f := range def.fields {
			if err := need(int(f.Size)); err != nil {
				return nil, err
			}
			m.Fields[f.Num] = data[:f.Size]
			m.types[f.Num] = f.BaseType
			data = data[f.Size:]
		}
		if err := need(def.devSize); err != nil {
			return nil, err
		}
		data = data[def.devSize:]
		// Field 253 is the timestamp of every message that has one.
		if ts, ok := m.uint(253); ok {
			lastTimestamp = uint32(ts)
		}
		if compressed {
			// The offset is the low 5 bits of the timestamp, rolling
			// over from the last full timestamp.
			offset := uint32(header & 0x1F)
			ts := lastTimestamp&^0x1F | offset
			if offset < lastTimestamp&0x1F {
				ts += 0x20
			}
			lastTimestamp = ts
			b := make([]byte, 4)
			def.order.PutUint32(b, ts)
			m.Fields[253] = b
			m.types[253] = Uint32
		}
		messages = append(messages, m)
	}
	return messages, nil
}
