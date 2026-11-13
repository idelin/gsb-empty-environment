package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type patternReader struct {
	next byte
}

func (r *patternReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.next
		r.next++
	}
	return len(p), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func makeRecords(n int) [][]byte {
	records := make([][]byte, n)
	for i := range records {
		record := make([]byte, RecordSize)
		for k := range record {
			record[k] = byte(i*37 + k*11 + 3)
		}
		record[0] = byte(i)
		record[1] = byte(i >> 8)
		records[i] = record
	}
	return records
}

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 1)
	}
	return key
}

type testEnv struct {
	records  [][]byte
	key      []byte
	db       *Database
	registry Registry
	first    *Server
	second   *Server
	client   *Client
	pub1     ed25519.PublicKey
	pub2     ed25519.PublicKey
	priv1    ed25519.PrivateKey
	priv2    ed25519.PrivateKey
}

func newTestEnv(t *testing.T, n int) *testEnv {
	t.Helper()

	records := makeRecords(n)
	key := testKey()
	db, err := Publish(records, key, "snapshot-2026-11-14")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	pub1, priv1, err := GenerateServerIdentity()
	if err != nil {
		t.Fatalf("GenerateServerIdentity: %v", err)
	}
	pub2, priv2, err := GenerateServerIdentity()
	if err != nil {
		t.Fatalf("GenerateServerIdentity: %v", err)
	}

	first, err := NewServer(db, priv1)
	if err != nil {
		t.Fatalf("NewServer(first): %v", err)
	}
	second, err := NewServer(db, priv2)
	if err != nil {
		t.Fatalf("NewServer(second): %v", err)
	}

	registry := db.Registry()
	client, err := NewClient(registry, key, [2]ed25519.PublicKey{pub1, pub2}, &patternReader{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	return &testEnv{
		records:  records,
		key:      key,
		db:       db,
		registry: registry,
		first:    first,
		second:   second,
		client:   client,
		pub1:     pub1,
		pub2:     pub2,
		priv1:    priv1,
		priv2:    priv2,
	}
}

func TestValidQueriesReturnCorrectRecords(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 2, 7, 8, 9, 17, 32, 37, 4096} {
		n := n
		t.Run("N="+itoa(n), func(t *testing.T) {
			t.Parallel()
			env := newTestEnv(t, n)

			var indices []int
			if n <= 37 {
				for j := 0; j < n; j++ {
					indices = append(indices, j)
				}
			} else {
				indices = []int{0, 1, n / 2, n - 2, n - 1}
			}

			for _, j := range indices {
				got, err := env.client.Query(j, env.first, env.second)
				if err != nil {
					t.Fatalf("Query(%d): %v", j, err)
				}
				if !bytes.Equal(got, env.records[j]) {
					t.Fatalf("Query(%d) returned %x, want %x", j, got, env.records[j])
				}
			}
		})
	}
}

func TestQueryVectorShapeAndTrailingBits(t *testing.T) {
	t.Parallel()

	for n := 1; n <= 129; n++ {
		q0, q1, err := GenerateQuery(bytes.NewReader(bytes.Repeat([]byte{0xff}, QueryLength(n))), n, n-1)
		if err != nil {
			t.Fatalf("N=%d GenerateQuery: %v", n, err)
		}
		if len(q0) != QueryLength(n) || QueryLength(n) != (n+7)/8 {
			t.Fatalf("N=%d query length=%d, want %d", n, len(q0), (n+7)/8)
		}
		if !validQuery(q0, n) || !validQuery(q1, n) {
			t.Fatalf("N=%d generated query is not valid", n)
		}
		for i := 0; i < n; i++ {
			want := i == n-1
			set := (q0[i/8]^q1[i/8])&(1<<uint(i%8)) != 0
			if set != want {
				t.Fatalf("N=%d bit %d of q0 XOR q1 is wrong", n, i)
			}
		}
	}
}

func TestServerRejectsMalformedQueryVectors(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 13)
	valid, _, err := GenerateQuery(bytes.NewReader(bytes.Repeat([]byte{0xaa}, QueryLength(13))), 13, 3)
	if err != nil {
		t.Fatal(err)
	}

	short := append([]byte(nil), valid[:len(valid)-1]...)
	if _, err := env.first.Respond(short); !errors.Is(err, ErrMalformedQuery) {
		t.Fatalf("short vector error = %v, want ErrMalformedQuery", err)
	}

	long := append(append([]byte(nil), valid...), 0)
	if _, err := env.first.Respond(long); !errors.Is(err, ErrMalformedQuery) {
		t.Fatalf("long vector error = %v, want ErrMalformedQuery", err)
	}

	nonZeroTail := append([]byte(nil), valid...)
	nonZeroTail[len(nonZeroTail)-1] |= 0x80
	if _, err := env.first.Respond(nonZeroTail); !errors.Is(err, ErrMalformedQuery) {
		t.Fatalf("non-zero trailing bit error = %v, want ErrMalformedQuery", err)
	}
}

func TestTagFormulaAndRowLayout(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	for i, record := range env.records {
		row, ok := env.db.Row(i)
		if !ok {
			t.Fatalf("row %d missing", i)
		}
		if !bytes.Equal(row[:RecordSize], record) {
			t.Fatalf("row %d record mismatch", i)
		}

		mac := hmac.New(sha256.New, env.key)
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(i))
		_, _ = mac.Write(prefix[:])
		_, _ = mac.Write(record)
		want := mac.Sum(nil)

		if !bytes.Equal(row[RecordSize:], want) {
			t.Fatalf("row %d tag does not match the independent HMAC computation", i)
		}
		if !VerifyRowTag(env.key, i, row[:]) {
			t.Fatalf("row %d tag rejected", i)
		}
		if VerifyRowTag(env.key, (i+1)%len(env.records), row[:]) {
			t.Fatalf("row %d tag accepted under the wrong index", i)
		}

		tampered := append([]byte(nil), row[:]...)
		tampered[0] ^= 0x80
		if VerifyRowTag(env.key, i, tampered) {
			t.Fatalf("tampered row %d tag accepted", i)
		}
	}
}

func TestPublishRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	key := testKey()
	valid := makeRecords(2)

	if _, err := Publish(nil, key, "snap"); err == nil {
		t.Fatal("empty database accepted")
	}
	if _, err := Publish(make([][]byte, MaxRecords+1), key, "snap"); err == nil {
		t.Fatal("oversized database accepted")
	}
	if _, err := Publish(valid, nil, "snap"); err == nil {
		t.Fatal("empty HMAC key accepted")
	}
	if _, err := Publish(valid, key, ""); err == nil {
		t.Fatal("empty snapshot ID accepted")
	}

	badRecord := make([][]byte, len(valid))
	copy(badRecord, valid)
	badRecord[0] = append([]byte(nil), valid[0][:RecordSize-1]...)
	if _, err := Publish(badRecord, key, "snap"); err == nil {
		t.Fatal("31-byte record accepted")
	}
}

func TestResponseSignaturesBindRequiredFields(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 11)
	q0, _, err := GenerateQuery(&patternReader{}, env.registry.N, 4)
	if err != nil {
		t.Fatal(err)
	}
	response, err := env.first.Respond(q0)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyResponseSignature(response, q0, env.registry.N) {
		t.Fatal("honest response signature did not verify")
	}

	tests := []struct {
		name   string
		mutate func(*Response)
	}{
		{"payload", func(r *Response) { r.Payload[7] ^= 0x01 }},
		{"snapshot", func(r *Response) { r.SnapshotID += "-other" }},
		{"signature", func(r *Response) { r.Signature[0] ^= 0x01 }},
		{"server ID", func(r *Response) { r.ServerID[0] ^= 0x01 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			altered := response.Clone()
			test.mutate(&altered)
			if VerifyResponseSignature(altered, q0, env.registry.N) {
				t.Fatalf("signature verified after %s mutation", test.name)
			}
		})
	}

	otherQuery, _, err := GenerateQuery(&patternReader{next: 99}, env.registry.N, 4)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyResponseSignature(response, otherQuery, env.registry.N) {
		t.Fatal("signature verified for a different query vector")
	}
}

func TestRegistryBindsSnapshotAndRoot(t *testing.T) {
	t.Parallel()

	records := makeRecords(8)
	key := testKey()
	db1, err := Publish(records, key, "snapshot-a")
	if err != nil {
		t.Fatal(err)
	}
	db2, err := Publish(records, key, "snapshot-b")
	if err != nil {
		t.Fatal(err)
	}
	if db1.MerkleRoot() != db2.MerkleRoot() {
		t.Fatal("the same rows should produce the same Merkle root")
	}
	if db1.SnapshotID() == db2.SnapshotID() {
		t.Fatal("snapshot IDs should differ")
	}

	changed := makeRecords(8)
	changed[3][9] ^= 0x01
	db3, err := Publish(changed, key, "snapshot-a")
	if err != nil {
		t.Fatal(err)
	}
	if db1.MerkleRoot() == db3.MerkleRoot() {
		t.Fatal("changing a row must change the Merkle root")
	}
}

func TestClientUsesOneUniformError(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 9)

	badIndex, err := env.client.Query(9, env.first, env.second)
	if badIndex != nil || err != ErrVerification {
		t.Fatalf("bad index = (%x, %v), want (nil, ErrVerification)", badIndex, err)
	}

	badRandomClient, err := NewClient(env.registry, env.key, [2]ed25519.PublicKey{env.pub1, env.pub2}, failingReader{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := badRandomClient.Query(0, env.first, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("failing random source = (%x, %v), want (nil, ErrVerification)", got, err)
	}

	if err == nil || err.Error() != ErrVerification.Error() {
		t.Fatal("query errors must not expose their cause")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
