// Package fittest decodes the FIT files package fit writes, for tests.
package fittest

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/fit"
)

// Message is a decoded data message: its global number and the raw bytes of
// each field by field number.
type Message struct {
	Global uint16
	Fields map[uint8][]byte
}

// Uint returns an unsigned integer field, failing the test if it's missing.
func (m Message) Uint(t testing.TB, num uint8) uint64 {
	t.Helper()
	b, ok := m.Fields[num]
	if !ok {
		t.Fatalf("message %d has no field %d", m.Global, num)
	}
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// Sint32 returns a signed 32 bit integer field.
func (m Message) Sint32(t testing.TB, num uint8) int32 {
	t.Helper()
	return int32(uint32(m.Uint(t, num)))
}

// String returns a string field, without its terminating zeros.
func (m Message) String(num uint8) string {
	return string(bytes.TrimRight(m.Fields[num], "\x00"))
}

// Decode decodes a FIT file with little endian definitions and normal
// headers, checking its header and CRCs.
func Decode(t testing.TB, file []byte) []Message {
	t.Helper()
	if len(file) < 16 || file[0] != 14 || string(file[8:12]) != ".FIT" {
		t.Fatalf("invalid header % x", file[:min(len(file), 14)])
	}
	if crc := binary.LittleEndian.Uint16(file[12:]); crc != fit.CRC(0, file[:12]) {
		t.Fatalf("header CRC %x, want %x", crc, fit.CRC(0, file[:12]))
	}
	size := int(binary.LittleEndian.Uint32(file[4:]))
	if len(file) != 14+size+2 {
		t.Fatalf("file is %d bytes, header says %d of data", len(file), size)
	}
	if crc := binary.LittleEndian.Uint16(file[14+size:]); crc != fit.CRC(0, file[:14+size]) {
		t.Fatalf("file CRC %x, want %x", crc, fit.CRC(0, file[:14+size]))
	}
	type definition struct {
		global uint16
		fields []fit.Field
	}
	defs := map[uint8]definition{}
	var messages []Message
	data := file[14 : 14+size]
	for len(data) > 0 {
		header := data[0]
		local := header & 0x0F
		data = data[1:]
		if header&0x40 != 0 {
			def := definition{global: binary.LittleEndian.Uint16(data[2:])}
			n := int(data[4])
			data = data[5:]
			for i := range n {
				def.fields = append(def.fields, fit.Field{Num: data[i*3], Size: data[i*3+1], BaseType: data[i*3+2]})
			}
			data = data[n*3:]
			defs[local] = def
			continue
		}
		def, ok := defs[local]
		if !ok {
			t.Fatalf("data message of undefined local type %d", local)
		}
		m := Message{Global: def.global, Fields: map[uint8][]byte{}}
		for _, field := range def.fields {
			m.Fields[field.Num] = data[:field.Size]
			data = data[field.Size:]
		}
		messages = append(messages, m)
	}
	return messages
}
