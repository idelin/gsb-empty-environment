package netsec

import (
	"bufio"
	"bytes"
	"testing"
)

func addFuzzSeeds(f *testing.F) {
	f.Add([]byte("set key 0 0 3\r\nabc\r\n"))
	f.Add([]byte("get a b\r\n"))
	f.Add([]byte("set key 0 0 3 noreply\r\nabcX\r\n"))
	f.Add([]byte("get bad\tkey\r\n"))
	f.Add([]byte("VALUE t1:a 0 1\r\na\r\nEND\r\n"))
	f.Add([]byte("VALUE t2:a 0 2\r\nab\r\nEND\r\n"))
	f.Add(makeBinaryFrame(binaryRequestMagic, 0x00, []byte("a"), nil, nil))
}

func FuzzNormalizeText(f *testing.F) {
	addFuzzSeeds(f)
	cfg := Config{MaxValueLength: 1024, MaxResponseLength: 4096}
	n, err := NewNormalizer("t1:", &cfg)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		req, err := n.NormalizeText(raw)
		if err != nil {
			return
		}
		if req == nil || len(req.Bytes) == 0 {
			t.Fatal("successful normalization returned an empty request")
		}
	})
}

func FuzzNormalizeBinary(f *testing.F) {
	addFuzzSeeds(f)
	cfg := Config{AllowBinary: true, MaxValueLength: 1024, MaxResponseLength: 4096}
	n, err := NewNormalizer("t1:", &cfg)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		req, err := n.NormalizeBinary(raw)
		if err != nil {
			return
		}
		if req == nil || len(req.Bytes) < binaryHeaderLength {
			t.Fatal("successful binary normalization returned an invalid frame")
		}
	})
}

func FuzzResponseFilter(f *testing.F) {
	addFuzzSeeds(f)
	cfg := Config{MaxValueLength: 1024, MaxResponseLength: 4096}
	n, err := NewNormalizer("t1:", &cfg)
	if err != nil {
		f.Fatal(err)
	}
	req, err := n.NormalizeText([]byte("get a\r\n"))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		br := bufio.NewReader(bytes.NewReader(raw))
		out, err := n.ReadResponse(br, req)
		if err != nil {
			return
		}
		if bytes.HasPrefix(out, []byte("VALUE t2:")) {
			t.Fatal("foreign tenant key leaked")
		}
	})
}
