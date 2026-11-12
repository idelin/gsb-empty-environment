package avroocf

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

func buildNestedSchema(levels int) string {
	var node any = "long"
	for level := levels - 1; level >= 0; level-- {
		node = map[string]any{
			"type":   "record",
			"name":   fmt.Sprintf("a.L%d", level),
			"fields": []any{map[string]any{"name": "child", "type": node}},
		}
	}
	raw, err := json.Marshal(node)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

type container struct {
	buffer bytes.Buffer
	sync   [syncSize]byte
}

func newContainer(t *testing.T, schemaJSON string, codec string) *container {
	t.Helper()
	c := &container{}
	if _, err := rand.Read(c.sync[:]); err != nil {
		t.Fatal(err)
	}
	c.buffer.Write(magic[:])
	metadata := map[string][]byte{
		"avro.schema": []byte(schemaJSON),
	}
	if codec != "" {
		metadata["avro.codec"] = []byte(codec)
	}
	writeLong(&c.buffer, int64(len(metadata)))
	for key, value := range metadata {
		writeString(&c.buffer, key)
		writeBytes(&c.buffer, value)
	}
	writeLong(&c.buffer, 0)
	c.buffer.Write(c.sync[:])
	return c
}

func (c *container) writeBlock(objects [][]byte, negativeCount bool, deflate bool) {
	var payload bytes.Buffer
	for _, object := range objects {
		payload.Write(object)
	}
	raw := payload.Bytes()
	if deflate {
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		_, _ = writer.Write(raw)
		_ = writer.Close()
		raw = compressed.Bytes()
	}
	count := int64(len(objects))
	if negativeCount {
		writeLong(&c.buffer, -count)
		writeLong(&c.buffer, int64(len(raw)))
	} else {
		writeLong(&c.buffer, count)
	}
	c.buffer.Write(raw)
	c.buffer.Write(c.sync[:])
}

func writeLong(buffer *bytes.Buffer, value int64) {
	zigzag := uint64(value<<1) ^ uint64(value>>63)
	for zigzag >= 0x80 {
		buffer.WriteByte(byte(zigzag) | 0x80)
		zigzag >>= 7
	}
	buffer.WriteByte(byte(zigzag))
}

func writeString(buffer *bytes.Buffer, value string) {
	writeBytes(buffer, []byte(value))
}

func writeBytes(buffer *bytes.Buffer, value []byte) {
	writeLong(buffer, int64(len(value)))
	buffer.Write(value)
}

func encodeLong(value int64) []byte {
	var buffer bytes.Buffer
	writeLong(&buffer, value)
	return buffer.Bytes()
}

func allowConfig(schema *Schema) Config {
	return Config{
		AllowedFingerprints: map[string]bool{schema.FingerprintSHA256(): true},
		MaxSchemaDepth:      8,
		MaxBlockObjects:     100,
		MaxBlockBytes:       1 << 20,
	}
}

func openContainer(t *testing.T, data []byte, config Config) *Reader {
	t.Helper()
	reader, err := Open(bytes.NewReader(data), config)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	return reader
}

func drainAll(t *testing.T, reader *Reader) []any {
	t.Helper()
	var decoded []any
	for {
		count, err := reader.NextBlock()
		if errors.Is(err, io.EOF) {
			return decoded
		}
		if err != nil {
			t.Fatalf("NextBlock failed: %v", err)
		}
		for index := int64(0); index < count; index++ {
			value, err := reader.Decode()
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			decoded = append(decoded, value)
		}
	}
}
