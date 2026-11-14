package netsec

import "testing"

func FuzzHPACKBlock(f *testing.F) {
	f.Add([]byte{0x82, 0x86, 0x84, 0x41, 0x0f, 'w', 'w', 'w', '.', 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm'}, uint8(7))
	f.Add([]byte{0x20, 0x40, 0x01, 'x', 0x01, 'v'}, uint8(2))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff}, uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, split uint8) {
		d := NewDecoder(Config{MaxCompressedBlockBytes: 1 << 20})
		cut := 0
		if len(data) > 0 {
			cut = int(split) % (len(data) + 1)
		}
		if cut == len(data) {
			_, _ = d.ProcessFrame(headersFrame(1, true, false, data))
			return
		}
		_, _ = d.ProcessFrame(headersFrame(1, false, false, data[:cut]))
		_, _ = d.ProcessFrame(continuationFrame(1, true, data[cut:]))
	})
}
