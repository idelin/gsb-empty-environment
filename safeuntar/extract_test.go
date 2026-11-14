package safeuntar

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// rawBuilder emits 512-byte tar blocks directly, which is necessary to craft
// hostile records (escaping PAX overrides, device nodes) that the standard
// library's Writer refuses to produce.
type rawBuilder struct {
	bytes.Buffer
}

func rawHeader(name string, typeflag byte, size int64, linkname string) []byte {
	block := make([]byte, 512)
	rightPaddedCopy(block[0:100], name)
	putOctal(block[100:108], 0o644)
	putOctal(block[108:116], 0)
	putOctal(block[116:124], 0)
	putOctal(block[124:136], size)
	putOctal(block[136:148], 0)
	block[156] = typeflag
	rightPaddedCopy(block[157:257], linkname)
	copy(block[257:263], "ustar\x00")
	copy(block[263:265], "00")
	putOctal(block[329:337], 0)
	putOctal(block[337:345], 0)
	fillChecksum(block)
	return block
}

// fillChecksum writes the POSIX header checksum: the sum of every header
// byte, treating the eight checksum bytes themselves as spaces.
func fillChecksum(block []byte) {
	for i := 148; i < 156; i++ {
		block[i] = ' '
	}
	var sum int
	for _, value := range block {
		sum += int(value)
	}
	// Six zero-padded octal digits, NUL, space.
	checksum := octalDigits(int64(sum))
	field := block[148:156]
	for i := range field {
		field[i] = '0'
	}
	start := 6 - len(checksum)
	if start < 0 {
		panic("tar checksum does not fit field")
	}
	copy(field[start:6], checksum)
	field[6] = 0
	field[7] = ' '
}

func octalDigits(value int64) []byte {
	if value == 0 {
		return []byte{'0'}
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%8)}, digits...)
		value /= 8
	}
	return digits
}

func rightPaddedCopy(field []byte, value string) {
	if len(value) >= len(field) {
		copy(field, value)
		return
	}
	copy(field, value)
	for i := len(value); i < len(field); i++ {
		field[i] = 0
	}
}

func putOctal(field []byte, value int64) {
	// NUL-terminated octal field, right aligned.
	if value == 0 {
		for i := 0; i < len(field)-1; i++ {
			field[i] = '0'
		}
		field[len(field)-1] = 0
		return
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%8)}, digits...)
		value /= 8
	}
	start := len(field) - 1 - len(digits)
	if start < 0 {
		panic("octal value does not fit field")
	}
	for i := 0; i < start; i++ {
		field[i] = '0'
	}
	copy(field[start:], digits)
	field[len(field)-1] = 0
}

func (b *rawBuilder) writeBlocks(data []byte) {
	b.Write(data)
	if padding := len(data) % 512; padding != 0 {
		b.Write(make([]byte, 512-padding))
	}
}

func (b *rawBuilder) rawEntry(name string, typeflag byte, payload []byte, linkname string) {
	b.Write(rawHeader(name, typeflag, int64(len(payload)), linkname))
	b.writeBlocks(payload)
}

// paxEntry emits an extended header whose records override the next entry's
// name or link target.
func (b *rawBuilder) paxEntry(records map[string]string, next []byte, nextPayload []byte) {
	var body strings.Builder
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		record := key + "=" + records[key] + "\n"
		body.WriteString(formatPAXRecord(record))
	}
	paxBody := []byte(body.String())
	b.Write(rawHeader("PaxHeaders/meta", tar.TypeXHeader, int64(len(paxBody)), ""))
	b.writeBlocks(paxBody)
	b.Write(next)
	b.writeBlocks(nextPayload)
}

// formatPAXRecord prefixes a PAX record with the correct "%d %s" length.
func formatPAXRecord(record string) string {
	for length := len(record) + 1; ; length++ {
		prefix := strconvItoa(length) + " "
		if len(prefix)+len(record) == length {
			return prefix + record
		}
	}
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func (b *rawBuilder) finish() {
	b.Write(make([]byte, 1024))
}

func buildTar(t *testing.T, entries []*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		if err := tw.WriteHeader(entry); err != nil {
			t.Fatalf("write header %q: %v", entry.Name, err)
		}
		if entry.Size > 0 {
			if _, err := tw.Write(bytes.Repeat([]byte{'A'}, int(entry.Size))); err != nil {
				t.Fatalf("write body %q: %v", entry.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func extractToTemp(t *testing.T, archive []byte) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(bytes.NewReader(archive), dest); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	return dest
}

func assertReject(t *testing.T, archive []byte, want error) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "out")
	err := Extract(bytes.NewReader(archive), dest)
	if !errors.Is(err, want) {
		t.Fatalf("Extract error = %v, want errors.Is(..., %v)", err, want)
	}
	if _, statErr := os.Lstat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination %q must not exist after failure, stat err = %v", dest, statErr)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".safeuntar-staging-") {
			t.Fatalf("staging directory was not cleaned up: %q", entry.Name())
		}
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}
