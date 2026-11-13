package netsec

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"testing"
)

func zlibCompress(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("compress write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("compress close: %v", err)
	}
	return buf.Bytes()
}

func wrapCompressed(algorithm uint16, uncompressedLength uint32, payload []byte) []byte {
	out := make([]byte, 0, 7+len(payload))
	out = append(out, byte(algorithm>>8), byte(algorithm))
	out = append(out, byte(uncompressedLength>>16), byte(uncompressedLength>>8), byte(uncompressedLength))
	length := len(payload)
	out = append(out, byte(length>>16), byte(length>>8), byte(length))
	out = append(out, payload...)
	return out
}

func buildCertificateMessage(context []byte, certs [][]byte, extensions [][]byte) []byte {
	var msg bytes.Buffer
	msg.WriteByte(byte(len(context)))
	msg.Write(context)

	var entries bytes.Buffer
	for i, der := range certs {
		ext := []byte{}
		if i < len(extensions) {
			ext = extensions[i]
		}
		entries.Write([]byte{byte(len(der) >> 16), byte(len(der) >> 8), byte(len(der))})
		entries.Write(der)
		entries.Write([]byte{byte(len(ext) >> 8), byte(len(ext))})
		entries.Write(ext)
	}
	list := entries.Bytes()
	msg.Write([]byte{byte(len(list) >> 16), byte(len(list) >> 8), byte(len(list))})
	msg.Write(list)
	return msg.Bytes()
}

func TestDecodeRoundTrip(t *testing.T) {
	leaf := []byte("fake-leaf-certificate-der")
	intermediate := bytes.Repeat([]byte{0xAB}, 300)
	ext := []byte{0x00, 0x11, 0x00, 0x00} // extension type 17, empty body
	message := buildCertificateMessage(nil, [][]byte{leaf, intermediate}, [][]byte{ext, nil})

	data := wrapCompressed(AlgorithmZlib, uint32(len(message)), zlibCompress(t, message))

	decoded, err := DecodeCompressedCertificate(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(decoded.Message, message) {
		t.Fatal("decompressed message does not match original")
	}
	if len(decoded.CertificateRequestContext) != 0 {
		t.Fatalf("unexpected context: %v", decoded.CertificateRequestContext)
	}
	if len(decoded.Certificates) != 2 {
		t.Fatalf("want 2 certs, got %d", len(decoded.Certificates))
	}
	if !bytes.Equal(decoded.Certificates[0], leaf) || !bytes.Equal(decoded.Certificates[1], intermediate) {
		t.Fatal("certificate bytes mismatch")
	}
}

func TestDecodeWithContextAndEmptyList(t *testing.T) {
	message := buildCertificateMessage([]byte("ctx"), nil, nil)
	data := wrapCompressed(AlgorithmZlib, uint32(len(message)), zlibCompress(t, message))

	decoded, err := DecodeCompressedCertificate(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(decoded.CertificateRequestContext, []byte("ctx")) {
		t.Fatalf("context mismatch: %q", decoded.CertificateRequestContext)
	}
	if len(decoded.Certificates) != 0 {
		t.Fatalf("want empty cert list, got %d", len(decoded.Certificates))
	}
}

func TestUnknownAlgorithm(t *testing.T) {
	message := buildCertificateMessage(nil, [][]byte{[]byte("der")}, nil)
	compressed := zlibCompress(t, message)

	for _, algorithm := range []uint16{2, 3, 65535} {
		data := wrapCompressed(algorithm, uint32(len(message)), compressed)
		_, err := DecodeCompressedCertificate(data)
		if !errors.Is(err, ErrUnknownAlgorithm) {
			t.Fatalf("algorithm %d: want ErrUnknownAlgorithm, got %v", algorithm, err)
		}
	}
}

func TestDeclaredLengthSmallerThanActual(t *testing.T) {
	// The core decompression-bomb case: a small declared length hides a stream
	// that expands far beyond it.
	plain := bytes.Repeat([]byte("bomb-"), 25000) // 125 KiB
	data := wrapCompressed(AlgorithmZlib, 10, zlibCompress(t, plain))

	_, err := DecodeCompressedCertificate(data)
	if !errors.Is(err, ErrDeclaredLengthExceeded) {
		t.Fatalf("want ErrDeclaredLengthExceeded, got %v", err)
	}
}

func TestDeclaredLengthLargerThanActual(t *testing.T) {
	plain := []byte("short")
	data := wrapCompressed(AlgorithmZlib, uint32(len(plain)+100), zlibCompress(t, plain))

	_, err := DecodeCompressedCertificate(data)
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("want ErrLengthMismatch, got %v", err)
	}
}

func TestDeclaredLengthOverAbsoluteLimit(t *testing.T) {
	plain := []byte("tiny")
	data := wrapCompressed(AlgorithmZlib, MaxUncompressedCertificate+1, zlibCompress(t, plain))

	_, err := DecodeCompressedCertificate(data)
	if !errors.Is(err, ErrAbsoluteLimitExceeded) {
		t.Fatalf("want ErrAbsoluteLimitExceeded, got %v", err)
	}
}

func TestActualOutputOverAbsoluteLimit(t *testing.T) {
	// Declared length is exactly at the ceiling, but the stream actually
	// produces one more byte. The hard cap must stop decompression.
	plain := make([]byte, MaxUncompressedCertificate+1)
	data := wrapCompressed(AlgorithmZlib, MaxUncompressedCertificate, zlibCompress(t, plain))

	_, err := DecodeCompressedCertificate(data)
	if !errors.Is(err, ErrAbsoluteLimitExceeded) {
		t.Fatalf("want ErrAbsoluteLimitExceeded, got %v", err)
	}
}

func TestTruncatedAndTrailing(t *testing.T) {
	message := buildCertificateMessage(nil, [][]byte{[]byte("der")}, nil)
	good := wrapCompressed(AlgorithmZlib, uint32(len(message)), zlibCompress(t, message))

	cases := map[string][]byte{
		"empty":         nil,
		"short header":  good[:7],
		"vector short":  good[:len(good)-3], // header promises more bytes than present
		"trailing byte": append(append([]byte{}, good...), 0xFF),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCompressedCertificate(data); !errors.Is(err, ErrTruncated) {
				t.Fatalf("want ErrTruncated, got %v", err)
			}
		})
	}
}

func TestInvalidZlibPayload(t *testing.T) {
	data := wrapCompressed(AlgorithmZlib, 5, []byte("not-a-zlib-stream"))
	if _, err := DecodeCompressedCertificate(data); !errors.Is(err, ErrDecompression) {
		t.Fatalf("want ErrDecompression, got %v", err)
	}
}

func TestCorruptedChecksum(t *testing.T) {
	message := buildCertificateMessage(nil, [][]byte{[]byte("der")}, nil)
	compressed := zlibCompress(t, message)
	compressed[len(compressed)-1] ^= 0xFF
	data := wrapCompressed(AlgorithmZlib, uint32(len(message)), compressed)

	if _, err := DecodeCompressedCertificate(data); !errors.Is(err, ErrDecompression) {
		t.Fatalf("want ErrDecompression, got %v", err)
	}
}

func TestMalformedCertificateMessage(t *testing.T) {
	// Valid compression and exact declared length, but the payload is not a
	// Certificate message: it cannot even supply a context length byte.
	plain := []byte{}
	data := wrapCompressed(AlgorithmZlib, 0, zlibCompress(t, plain))

	if _, err := DecodeCompressedCertificate(data); !errors.Is(err, ErrMalformedCertificate) {
		t.Fatalf("want ErrMalformedCertificate, got %v", err)
	}
}

func TestMalformedCertificateTruncatedEntry(t *testing.T) {
	plain := []byte{0x00, 0x00, 0x00, 0x05, 0x00, 0x00, 0x00, 0x03, 'a', 'b'}
	data := wrapCompressed(AlgorithmZlib, uint32(len(plain)), zlibCompress(t, plain))

	if _, err := DecodeCompressedCertificate(data); !errors.Is(err, ErrMalformedCertificate) {
		t.Fatalf("want ErrMalformedCertificate, got %v", err)
	}
}

func TestParseOuterMessage(t *testing.T) {
	payload := []byte{1, 2, 3, 4}
	data := wrapCompressed(AlgorithmZlib, 99, payload)

	cc, err := ParseCompressedCertificate(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cc.Algorithm != AlgorithmZlib || cc.UncompressedLength != 99 || !bytes.Equal(cc.Compressed, payload) {
		t.Fatalf("unexpected parse result: %+v", cc)
	}
}

func TestErrorWrappingUsesSentinels(t *testing.T) {
	data := wrapCompressed(42, 1, zlibCompress(t, []byte("x")))
	_, err := DecodeCompressedCertificate(data)
	if err == nil || err == ErrUnknownAlgorithm {
		t.Fatalf("expected wrapped error, got %v", err)
	}
	if !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("errors.Is failed: %v", err)
	}
	if fmt.Sprint(err) == ErrUnknownAlgorithm.Error() {
		t.Fatal("error should carry algorithm context, not be the bare sentinel")
	}
}
