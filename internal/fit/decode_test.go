package fit

import (
	"bytes"
	"testing"
)

// TestDecodeCompressedTimestamps decodes records with compressed timestamp
// headers, which watches use to save space, rolling over the 5 bit offset.
func TestDecodeCompressedTimestamps(t *testing.T) {
	t.Parallel()
	var w Writer
	w.Define(0, recordMessage, Uint32Field(253), Uint8Field(3))
	w.Data(0, uint32(1000), uint8(120))
	// The same local type without its timestamp, for compressed headers.
	w.Define(1, recordMessage, Uint8Field(3))
	// 1000 is 0b1111101000: an offset of 8. 10 is 2 seconds later, and 3
	// rolls over to 1027.
	for _, c := range []struct{ offset, heartRate uint8 }{{10, 121}, {3, 122}} {
		w.buf.WriteByte(0x80 | 1<<5 | c.offset)
		w.buf.WriteByte(c.heartRate)
	}
	var file bytes.Buffer
	if err := w.WriteFile(&file); err != nil {
		t.Fatal(err)
	}
	messages, err := Decode(file.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	var got []uint64
	for _, m := range messages {
		ts, _ := m.Uint(253)
		got = append(got, ts)
	}
	if len(got) != 3 || got[0] != 1000 || got[1] != 1002 || got[2] != 1027 {
		t.Errorf("got timestamps %v, want 1000, 1002 and 1027", got)
	}
}
