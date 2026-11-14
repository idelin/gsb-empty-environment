package netsec

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustDecode(t *testing.T, d *Decoder, f Frame) Result {
	t.Helper()
	res, err := d.ProcessFrame(f)
	if err != nil {
		t.Fatalf("ProcessFrame(%s stream %d): %v", f.Type, f.StreamID, err)
	}
	return res
}

func TestHuffmanRoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "www.example.com", "Bearer secret", strings.Repeat("x", 300)} {
		encoded := HuffmanEncode(nil, s)
		decoded, err := HuffmanDecodeToString(encoded)
		if err != nil {
			t.Fatalf("decode %q: %v", s, err)
		}
		if decoded != s {
			t.Fatalf("Huffman round trip: got %q want %q", decoded, s)
		}
	}
	if _, err := HuffmanDecodeToString([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected invalid Huffman padding to fail")
	}
	// RFC 7541 C.6.1.
	want := []byte{0xf1, 0xe3, 0xc2, 0xe5, 0xf2, 0x3a, 0x6b, 0xa0, 0xab, 0x90, 0xf4, 0xff}
	if got := HuffmanEncode(nil, "www.example.com"); !reflect.DeepEqual(got, want) {
		t.Fatalf("RFC Huffman vector = %x, want %x", got, want)
	}
	if got, err := HuffmanDecodeToString(want); err != nil || got != "www.example.com" {
		t.Fatalf("RFC Huffman decode = %q, %v", got, err)
	}
}

func TestRFC7541RequestVectors(t *testing.T) {
	// RFC 7541 C.3.1 and C.3.2 request examples.
	first := []byte{
		0x82, 0x86, 0x84, 0x41, 0x0f,
		'w', 'w', 'w', '.', 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',
	}
	d := NewDecoder(Config{})
	res := mustDecode(t, d, headersFrame(1, true, false, first))
	want := []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "http"},
		{Name: ":path", Value: "/"},
		{Name: ":authority", Value: "www.example.com"},
	}
	if !reflect.DeepEqual(res.Fields, want) {
		t.Fatalf("first request = %#v", res.Fields)
	}

	second := []byte{0x82, 0x86, 0x84, 0xbe, 0x58, 0x08, 'n', 'o', '-', 'c', 'a', 'c', 'h', 'e'}
	res = mustDecode(t, d, headersFrame(3, true, false, second))
	want = append(want, HeaderField{Name: "cache-control", Value: "no-cache"})
	if !reflect.DeepEqual(res.Fields, want) {
		t.Fatalf("second request = %#v", res.Fields)
	}
}

func TestAllStaticTableIndicesDecode(t *testing.T) {
	for i := uint64(1); i <= staticTableLen; i++ {
		d := NewDecoder(Config{})
		res := mustDecode(t, d, headersFrame(1, true, false, testIndexed(nil, i)))
		if len(res.Fields) != 1 || res.Fields[0].Name == "" {
			t.Fatalf("static index %d decoded as %#v", i, res.Fields)
		}
	}
}

func TestLargeHuffmanStringLengthAndValue(t *testing.T) {
	value := strings.Repeat("x", 300)
	block := testLiteralIncremental(nil, "x-large", 0, value, true)
	d := NewDecoder(Config{})
	// Split inside the Huffman payload as well as the multi-byte length.
	res := mustDecode(t, d, headersFrame(1, false, false, block[:1]))
	res = mustDecode(t, d, continuationFrame(1, false, block[1:100]))
	res = mustDecode(t, d, continuationFrame(1, true, block[100:]))
	if len(res.Fields) != 1 {
		t.Fatalf("large Huffman decode produced %d fields", len(res.Fields))
	}
	if res.Fields[0].Value != value {
		t.Fatalf("large Huffman value length = %d", len(res.Fields[0].Value))
	}
}

func TestCanceledBlockAuthorizationIndexDoesNotPolluteLaterRequest(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})

	safe := testLiteralIncremental(nil, "x-safe", 0, "tenant-a-value", false)
	res := mustDecode(t, d, headersFrame(1, true, true, safe))
	if len(res.Fields) != 1 || res.Fields[0].Name != "x-safe" {
		t.Fatalf("safe block fields = %#v", res.Fields)
	}
	if got := d.DynamicTable().Entries(); len(got) != 1 || got[0].Name != "x-safe" {
		t.Fatalf("dynamic table after safe block = %#v", got)
	}

	// The attacker inserts Authorization and then indexes the just-inserted
	// entry inside the same block. The block is fragmented and canceled
	// before END_HEADERS, so none of those table changes may be published.
	evil := testTableSizeUpdate(nil, 1024)
	evil = append(evil, testLiteralIncremental(nil, "authorization", 0, "Bearer attacker-token", false)...)
	evil = append(evil, testIndexed(nil, 62)...)
	cuts := []int{3, 9, 20, len(evil)}
	prev := 0
	res = mustDecode(t, d, headersFrame(3, false, false, evil[prev:cuts[0]]))
	if !res.Pending {
		t.Fatal("first fragment should be pending")
	}
	prev = cuts[0]
	res = mustDecode(t, d, continuationFrame(3, false, evil[prev:cuts[1]]))
	if !res.Pending {
		t.Fatal("second fragment should be pending")
	}
	prev = cuts[1]
	res = mustDecode(t, d, continuationFrame(3, false, evil[prev:cuts[2]]))
	if got := d.DynamicTable(); got.Len() != 1 || got.Entries()[0].Name != "x-safe" {
		t.Fatalf("fragment was committed before END_HEADERS: %#v", got.Entries())
	}

	res, err := d.ProcessFrame(Frame{
		Type:     FrameRSTStream,
		StreamID: 3,
		Payload:  []byte{0, 0, 0, 8}, // CANCEL
	})
	if err != nil {
		t.Fatalf("RST_STREAM: %v", err)
	}
	if !res.Canceled || !res.Discarded {
		t.Fatalf("RST result = %#v", res)
	}

	// The canceled block still has to consume its CONTINUATION frames to
	// preserve connection-level framing; they must not commit anything.
	prev = cuts[2]
	res = mustDecode(t, d, continuationFrame(3, true, evil[prev:]))
	if !res.Canceled || !res.Discarded || len(res.Fields) != 0 {
		t.Fatalf("canceled completion result = %#v", res)
	}

	// Index 62 now resolves to the last safe entry, not Authorization.
	res = mustDecode(t, d, headersFrame(5, true, false, testIndexed(nil, 62)))
	if len(res.Fields) != 1 {
		t.Fatalf("later request fields = %#v", res.Fields)
	}
	if res.Fields[0].Name != "x-safe" || res.Fields[0].Value != "tenant-a-value" {
		t.Fatalf("canceled block polluted dynamic table: got %#v", res.Fields[0])
	}
	table := d.DynamicTable().Entries()
	if len(table) != 1 || table[0].Name != "x-safe" {
		t.Fatalf("dynamic table after canceled block = %#v", table)
	}
	if d.DynamicTable().MaxSize() != 4096 {
		t.Fatalf("canceled table size update leaked: max=%d", d.DynamicTable().MaxSize())
	}

	commits := d.Commits()
	if len(commits) != 2 {
		t.Fatalf("commit count = %d, want 2 (safe request and later request)", len(commits))
	}
	for _, c := range commits {
		for _, op := range c.Operations {
			if op.Field.Name == "authorization" {
				t.Fatalf("canceled Authorization mutation was committed: %#v", c)
			}
		}
	}
}

func TestLegalCrossFrameIndexedReference(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	block := testLiteralIncremental(nil, "x-a", 0, "one", false)
	block = append(block, testIndexed(nil, 62)...)

	// Split inside the literal name, inside its value, and inside the
	// indexed representation in the following frame.
	parts := [][]byte{block[:2], block[2:5], block[5:9], block[9:]}
	res := mustDecode(t, d, headersFrame(1, false, false, parts[0]))
	if !res.Pending {
		t.Fatal("HEADERS should be pending")
	}
	for i := 1; i < len(parts)-1; i++ {
		res = mustDecode(t, d, continuationFrame(1, false, parts[i]))
		if !res.Pending {
			t.Fatalf("fragment %d should be pending", i)
		}
	}
	res = mustDecode(t, d, continuationFrame(1, true, parts[len(parts)-1]))
	if len(res.Fields) != 2 {
		t.Fatalf("fields = %#v", res.Fields)
	}
	if res.Fields[0].Name != "x-a" || res.Fields[0].Value != "one" ||
		res.Fields[1].Name != "x-a" || res.Fields[1].Value != "one" {
		t.Fatalf("cross-frame index decoded incorrectly: %#v", res.Fields)
	}
	if res.Commit == nil || len(res.Commit.Operations) != 1 {
		t.Fatalf("commit = %#v", res.Commit)
	}
}

func TestEveryFrameSplitPosition(t *testing.T) {
	block := testTableSizeUpdate(nil, 4096)
	block = append(block, testLiteralIncremental(nil, "x-split", 0, "value", true)...)
	block = append(block, testIndexed(nil, 62)...)
	for split := 0; split <= len(block); split++ {
		d := NewDecoder(Config{})
		res := mustDecode(t, d, headersFrame(1, false, false, block[:split]))
		if !res.Pending {
			t.Fatalf("split %d: HEADERS was not pending", split)
		}
		res = mustDecode(t, d, continuationFrame(1, true, block[split:]))
		if len(res.Fields) != 2 || res.Fields[1].Name != "x-split" {
			t.Fatalf("split %d: fields = %#v", split, res.Fields)
		}
	}
}

func TestDynamicTableSizeUpdateIsTransactional(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	block := testTableSizeUpdate(nil, 0)
	block = append(block, testLiteralIncremental(nil, "x-temp", 0, "v", false)...)
	res := mustDecode(t, d, headersFrame(1, true, false, block))
	if len(res.Fields) != 1 || res.Fields[0].Name != "x-temp" {
		t.Fatalf("fields = %#v", res.Fields)
	}
	table := d.DynamicTable()
	if table.MaxSize() != 0 || table.Len() != 0 {
		t.Fatalf("table after size-0 update: max=%d len=%d", table.MaxSize(), table.Len())
	}
	if res.Commit == nil || res.Commit.MaxSize != 0 {
		t.Fatalf("commit = %#v", res.Commit)
	}

	block = testTableSizeUpdate(nil, 4096)
	block = append(block, testLiteralIncremental(nil, "x-z", 0, "ok", false)...)
	res = mustDecode(t, d, headersFrame(3, true, false, block))
	if res.Commit == nil || res.Commit.MaxSize != 4096 || len(res.Commit.Operations) != 2 {
		t.Fatalf("second commit = %#v", res.Commit)
	}

	// A size update larger than SETTINGS_HEADER_TABLE_SIZE is a compression
	// error, and the preceding mutation in the same block must not leak.
	bad := NewDecoder(Config{MaxDynamicTableSize: 4096})
	oversize := testTableSizeUpdate(nil, 8192)
	oversize = append(oversize, testLiteralIncremental(nil, "x-leak", 0, "v", false)...)
	_, err := bad.ProcessFrame(headersFrame(1, true, false, oversize))
	if err == nil {
		t.Fatal("expected oversized table update error")
	}
	if !errors.Is(err, ErrDynamicTableSize) {
		t.Fatalf("error = %v, want ErrDynamicTableSize", err)
	}
	if bad.DynamicTable().Len() != 0 || len(bad.Commits()) != 0 {
		t.Fatalf("oversized update leaked state: %#v", bad.DynamicTable().Entries())
	}

	// A size update after a field is also invalid.
	late := NewDecoder(Config{MaxDynamicTableSize: 4096})
	lateBlock := testLiteralIncremental(nil, "x-first", 0, "v", false)
	lateBlock = append(lateBlock, testTableSizeUpdate(nil, 0)...)
	_, err = late.ProcessFrame(headersFrame(1, true, false, lateBlock))
	if err == nil || !errors.Is(err, ErrDynamicTableSize) {
		t.Fatalf("late table update error = %v", err)
	}
	if late.DynamicTable().Len() != 0 || len(late.Commits()) != 0 {
		t.Fatal("late table update committed earlier field")
	}
}

func TestOversizedHuffmanStringRollsBackWholeBlock(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096, MaxStringLength: 8})
	safe := testLiteralIncremental(nil, "x-base", 0, "base", false)
	mustDecode(t, d, headersFrame(1, true, false, safe))
	if table := d.DynamicTable(); table.Len() != 1 || table.Entries()[0].Name != "x-base" {
		t.Fatalf("initial table = %#v", table.Entries())
	}

	bad := testLiteralIncremental(nil, "x-temp", 0, "v", false)
	bad = append(bad, testLiteralWithoutIndexing(nil, "x-long", 0, "abcdefghijklmnopqrstuvwxyz", true)...)
	_, err := d.ProcessFrame(headersFrame(3, true, false, bad))
	if err == nil {
		t.Fatal("expected oversized Huffman value error")
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("error = %v, want ErrLimitExceeded", err)
	}
	if len(d.Commits()) != 1 {
		t.Fatalf("bad block committed: commits=%d", len(d.Commits()))
	}
	// x-temp was decoded into the transactional clone before the oversized
	// value failed, so it must have been rolled back.
	table := d.DynamicTable()
	if table.Len() != 1 || table.Entries()[0].Name != "x-base" {
		t.Fatalf("rollback failed: %#v", table.Entries())
	}
	res := mustDecode(t, d, headersFrame(5, true, false, testIndexed(nil, 62)))
	if res.Fields[0].Name != "x-base" {
		t.Fatalf("index 62 after rollback = %#v", res.Fields)
	}
}

func TestPseudoHeaderAfterRegularHeaderRejected(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	block := testLiteralIncremental(nil, "x-regular", 0, "1", false)
	block = append(block, testLiteralWithoutIndexing(nil, ":path", 4, "/", false)...)
	_, err := d.ProcessFrame(headersFrame(1, true, false, block))
	if err == nil {
		t.Fatal("expected pseudo-header order error")
	}
	if !errors.Is(err, ErrPseudoHeaderOrder) {
		t.Fatalf("error = %v, want ErrPseudoHeaderOrder", err)
	}
	if se, ok := AsStreamError(err); !ok || se.StreamID != 1 {
		t.Fatalf("expected stream error, got %T %v", err, err)
	}
	if len(d.Commits()) != 0 || d.DynamicTable().Len() != 0 {
		t.Fatal("rejected block committed table state")
	}

	// A stream-scoped semantic error must not poison the connection.
	res := mustDecode(t, d, headersFrame(3, true, false, testLiteralIncremental(nil, "x-ok", 0, "v", false)))
	if res.Fields[0].Name != "x-ok" {
		t.Fatalf("connection unusable after stream error: %#v", res.Fields)
	}
}

func TestSensitiveHeadersCannotBeIndexed(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	_, err := d.ProcessFrame(headersFrame(1, true, false, testLiteralIncremental(nil, "authorization", 0, "Bearer x", false)))
	if err == nil || !errors.Is(err, ErrSensitiveHeaderIndexed) {
		t.Fatalf("error = %v, want ErrSensitiveHeaderIndexed", err)
	}
	if len(d.Commits()) != 0 || d.DynamicTable().Len() != 0 {
		t.Fatal("indexed Authorization was committed")
	}

	never := testLiteralNeverIndexed(nil, "authorization", 23, "Bearer allowed-only-here", false)
	res := mustDecode(t, d, headersFrame(3, true, false, never))
	if len(res.Fields) != 1 || !res.Fields[0].Sensitive {
		t.Fatalf("never-indexed field = %#v", res.Fields)
	}
	if d.DynamicTable().Len() != 0 {
		t.Fatal("never-indexed Authorization entered the dynamic table")
	}
}

func TestHeaderNameAndDuplicatePseudoRules(t *testing.T) {
	d := NewDecoder(Config{})
	_, err := d.ProcessFrame(headersFrame(1, true, false, testLiteralIncremental(nil, "X-Test", 0, "v", false)))
	if err == nil || !errors.Is(err, ErrUppercaseHeader) {
		t.Fatalf("uppercase error = %v", err)
	}
	if len(d.Commits()) != 0 || d.DynamicTable().Len() != 0 {
		t.Fatal("uppercase name committed")
	}

	d = NewDecoder(Config{})
	dup := append(testIndexed(nil, 4), testIndexed(nil, 4)...)
	_, err = d.ProcessFrame(headersFrame(1, true, false, dup))
	if err == nil || !errors.Is(err, ErrDuplicateHeader) {
		t.Fatalf("duplicate pseudo error = %v", err)
	}
	if len(d.Commits()) != 0 {
		t.Fatal("duplicate pseudo-header committed")
	}
}

func TestCompressedAndDecompressedBudgets(t *testing.T) {
	d := NewDecoder(Config{MaxCompressedBlockBytes: 4})
	fragment := testLiteralIncremental(nil, "x-a", 0, "v", false)
	if len(fragment) <= 4 {
		t.Fatal("test fragment unexpectedly small")
	}
	_, err := d.ProcessFrame(headersFrame(1, false, false, fragment))
	if err == nil || !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("compressed budget error = %v", err)
	}
	// Consume the rest of the frame sequence without committing.
	if _, err := d.ProcessFrame(continuationFrame(1, true, nil)); err == nil {
		t.Fatal("expected the pending compressed budget error")
	}
	if len(d.Commits()) != 0 || d.DynamicTable().Len() != 0 {
		t.Fatal("compressed budget overrun committed state")
	}

	d = NewDecoder(Config{MaxDecompressedBytes: 5})
	block := testLiteralIncremental(nil, "x-a", 0, "value", false)
	_, err = d.ProcessFrame(headersFrame(1, true, false, block))
	if err == nil || !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("decompressed budget error = %v", err)
	}
	if len(d.Commits()) != 0 || d.DynamicTable().Len() != 0 {
		t.Fatal("decompressed budget overrun committed state")
	}
}

func TestContextPoolResetsForTenantSwitchOnSameSocket(t *testing.T) {
	pool := NewContextPool(Config{MaxDynamicTableSize: 4096})
	ctxA := SecurityContext{PeerConnectionID: "socket-9", TenantID: "tenant-a", SettingsGeneration: 1}
	decoderA, err := pool.Acquire(ctxA, nil)
	if err != nil {
		t.Fatal(err)
	}
	blockA := testLiteralIncremental(nil, "x-a", 0, "a", false)
	mustDecode(t, decoderA, headersFrame(1, true, false, blockA))

	ctxB := SecurityContext{PeerConnectionID: "socket-9", TenantID: "tenant-b", SettingsGeneration: 2}
	if _, err := pool.Acquire(ctxB, nil); err == nil || !errors.Is(err, ErrSettingsNotSynchronized) {
		t.Fatalf("pool tenant switch without SETTINGS = %v", err)
	}
	settingsB := &SettingsSync{
		PeerConnectionID:   "socket-9",
		TenantID:           "tenant-b",
		Generation:         2,
		HeaderTableSize:    4096,
		HasHeaderTableSize: true,
		Synchronized:       true,
	}
	decoderB, err := pool.Acquire(ctxB, settingsB)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoderB.DynamicTable().Entries(); len(got) != 0 {
		t.Fatalf("tenant B inherited %#v", got)
	}
	blockB := testLiteralIncremental(nil, "x-b", 0, "b", false)
	mustDecode(t, decoderB, headersFrame(3, true, false, blockB))
}

func TestWireSettingsSyncAndTableSize(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	if err := d.BindTenant("peer-1", "tenant-a", 0); err != nil {
		t.Fatal(err)
	}
	// SETTINGS_HEADER_TABLE_SIZE = 1024.
	payload := []byte{0, 1, 0, 0, 4, 0}
	if err := d.ProcessSettings(payload, false); err != nil {
		t.Fatal(err)
	}
	if d.SettingsGeneration() != 1 {
		t.Fatalf("generation = %d", d.SettingsGeneration())
	}
	if d.SynchronizedSettingsGeneration() != 0 {
		t.Fatalf("unsynchronized SETTINGS advanced sync generation to %d", d.SynchronizedSettingsGeneration())
	}
	if err := d.ProcessSettings(nil, true); err != nil {
		t.Fatal(err)
	}
	if d.SynchronizedSettingsGeneration() != 1 {
		t.Fatalf("ACK did not synchronize: %d", d.SynchronizedSettingsGeneration())
	}
	if d.DynamicTable().AllowedMaxSize() != 1024 {
		t.Fatalf("allowed table size = %d", d.DynamicTable().AllowedMaxSize())
	}
	res := mustDecode(t, d, headersFrame(1, true, false, append(testTableSizeUpdate(nil, 1024), testLiteralIncremental(nil, "x-a", 0, "v", false)...)))
	if res.Commit == nil || res.Commit.AllowedMaxSize != 1024 {
		t.Fatalf("commit = %#v", res.Commit)
	}

	// A same-tenant SETTINGS generation without HEADER_TABLE_SIZE must not
	// reset or zero the existing compression context.
	ctx2 := SecurityContext{PeerConnectionID: "peer-1", TenantID: "tenant-a", SettingsGeneration: 2}
	if err := d.ApplySettings(SettingsSync{
		PeerConnectionID: "peer-1",
		TenantID:         "tenant-a",
		Generation:       2,
		Synchronized:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.BindContext(ctx2); err != nil {
		t.Fatal(err)
	}
	if got := d.DynamicTable(); got.MaxSize() != 1024 || got.Len() != 1 {
		t.Fatalf("same-tenant generation change damaged context: max=%d len=%d", got.MaxSize(), got.Len())
	}
}

func TestUppercaseOptionIsExplicit(t *testing.T) {
	block := testLiteralIncremental(nil, "X-Test", 0, "v", false)
	d := NewDecoder(Config{AllowUppercaseNames: true})
	res := mustDecode(t, d, headersFrame(1, true, false, block))
	if len(res.Fields) != 1 || res.Fields[0].Name != "X-Test" {
		t.Fatalf("uppercase option fields = %#v", res.Fields)
	}
}

func TestZeroValueDecoderUsesDefaults(t *testing.T) {
	var d Decoder
	res := mustDecode(t, &d, headersFrame(1, true, false, testIndexed(nil, 2)))
	if len(res.Fields) != 1 || res.Fields[0].Value != "GET" {
		t.Fatalf("zero-value decoder = %#v", res.Fields)
	}
	if d.Config().MaxDynamicTableSize != defaultDynamicTableSize {
		t.Fatalf("zero-value config = %#v", d.Config())
	}
}

func TestTwoTenantsOnSamePeerConnectionRequireSettingsReset(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	ctxA := SecurityContext{PeerConnectionID: "socket-7", TenantID: "tenant-a", SettingsGeneration: 1}
	if err := d.BindContext(ctxA); err != nil {
		t.Fatalf("bind A: %v", err)
	}
	safeA := testLiteralIncremental(nil, "x-tenant-a", 0, "a-secret", false)
	mustDecode(t, d, headersFrame(1, true, false, safeA))

	ctxB := SecurityContext{PeerConnectionID: "socket-7", TenantID: "tenant-b", SettingsGeneration: 2}
	if err := d.BindContext(ctxB); err == nil {
		t.Fatal("tenant switch without synchronized SETTINGS was accepted")
	} else if !errors.Is(err, ErrSettingsNotSynchronized) {
		t.Fatalf("switch error = %v", err)
	}
	if d.Context() != ctxA || d.DynamicTable().Len() != 1 {
		t.Fatal("failed tenant switch damaged tenant A context")
	}

	if err := d.ApplySettings(SettingsSync{
		PeerConnectionID:   "socket-7",
		TenantID:           "tenant-b",
		Generation:         2,
		HeaderTableSize:    4096,
		HasHeaderTableSize: true,
		Synchronized:       true,
	}); err != nil {
		t.Fatalf("apply B settings: %v", err)
	}
	if err := d.BindContext(ctxB); err != nil {
		t.Fatalf("bind B after SETTINGS: %v", err)
	}
	if d.DynamicTable().Len() != 0 {
		t.Fatalf("tenant B inherited tenant A dynamic table: %#v", d.DynamicTable().Entries())
	}
	safeB := testLiteralIncremental(nil, "x-tenant-b", 0, "b-secret", false)
	res := mustDecode(t, d, headersFrame(3, true, false, safeB))
	if res.Fields[0].Name != "x-tenant-b" {
		t.Fatalf("tenant B fields = %#v", res.Fields)
	}

	// Returning to tenant A must also reset, even though it previously
	// owned entries on this socket.
	ctxA2 := SecurityContext{PeerConnectionID: "socket-7", TenantID: "tenant-a", SettingsGeneration: 3}
	if err := d.BindContext(ctxA2); err == nil {
		t.Fatal("tenant A switch without synchronized SETTINGS was accepted")
	}
	if err := d.ApplySettings(SettingsSync{
		PeerConnectionID: "socket-7",
		TenantID:         "tenant-a",
		Generation:       3,
		Synchronized:     true,
	}); err != nil {
		t.Fatalf("apply A settings: %v", err)
	}
	if err := d.BindContext(ctxA2); err != nil {
		t.Fatalf("bind A again: %v", err)
	}
	if d.DynamicTable().Len() != 0 {
		t.Fatalf("tenant A inherited tenant B entries: %#v", d.DynamicTable().Entries())
	}
}

func TestStreamIDReuseAfterTenantResetCannotTakeLateContinuation(t *testing.T) {
	d := NewDecoder(Config{})
	ctxA := SecurityContext{PeerConnectionID: "socket-11", TenantID: "tenant-a", SettingsGeneration: 1}
	if err := d.BindContext(ctxA); err != nil {
		t.Fatal(err)
	}
	mustDecode(t, d, headersFrame(3, false, false, []byte{0x40}))
	if _, err := d.ProcessRSTStream(3, uint32(CodeCancel)); err != nil {
		t.Fatal(err)
	}
	mustDecode(t, d, continuationFrame(3, true, nil))

	ctxB := SecurityContext{PeerConnectionID: "socket-11", TenantID: "tenant-b", SettingsGeneration: 2}
	if err := d.ApplySettings(SettingsSync{
		PeerConnectionID: "socket-11",
		TenantID:         "tenant-b",
		Generation:       2,
		Synchronized:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.BindContext(ctxB); err != nil {
		t.Fatal(err)
	}
	// The old stream ID is still consumed on the underlying connection even
	// though the tenant's HPACK context was reset.
	if _, err := d.DecodeHeaderBlock(3, testIndexed(nil, 2), false); err == nil || !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("reused stream ID error = %v", err)
	}
	// A fresh, higher stream ID is still accepted.
	mustDecode(t, d, headersFrame(5, true, false, testIndexed(nil, 2)))
	if _, err := d.ProcessFrame(continuationFrame(3, true, []byte{0x40})); err == nil || !errors.Is(err, ErrUnexpectedContinuation) {
		t.Fatalf("late CONTINUATION error = %v", err)
	}
}

func TestConnectionFrameOrderAndLateContinuation(t *testing.T) {
	d := NewDecoder(Config{})
	mustDecode(t, d, headersFrame(1, false, false, []byte{0x40}))
	_, err := d.ProcessFrame(Frame{Type: FrameData, StreamID: 1, Payload: []byte("x")})
	if err == nil || !errors.Is(err, ErrFrameOrder) {
		t.Fatalf("interleaved DATA error = %v", err)
	}
	if !d.Closed() {
		t.Fatal("connection error did not close decoder")
	}

	d = NewDecoder(Config{})
	mustDecode(t, d, headersFrame(1, false, false, []byte{0x40}))
	_, err = d.ProcessFrame(continuationFrame(2, true, []byte{0x00}))
	if err == nil || !errors.Is(err, ErrFrameOrder) {
		t.Fatalf("wrong-stream CONTINUATION error = %v", err)
	}

	d = NewDecoder(Config{})
	_, err = d.ProcessFrame(continuationFrame(9, true, []byte{0x00}))
	if err == nil || !errors.Is(err, ErrUnexpectedContinuation) {
		t.Fatalf("orphan CONTINUATION error = %v", err)
	}

	// A late CONTINUATION must not attach to a newly opened stream. Opening
	// another HEADERS before the canceled block consumed END_HEADERS is a
	// connection error.
	d = NewDecoder(Config{})
	mustDecode(t, d, headersFrame(1, false, false, []byte{0x40}))
	if _, err := d.ProcessFrame(Frame{Type: FrameRSTStream, StreamID: 1, Payload: []byte{0, 0, 0, 8}}); err != nil {
		t.Fatalf("cancel stream 1: %v", err)
	}
	_, err = d.ProcessFrame(headersFrame(2, true, false, testIndexed(nil, 62)))
	if err == nil || !errors.Is(err, ErrFrameOrder) {
		t.Fatalf("HEADERS while canceled block is open = %v", err)
	}

	// After the canceled block completes, another CONTINUATION for the old
	// stream is an orphan, never a fragment of a new stream.
	d = NewDecoder(Config{})
	mustDecode(t, d, headersFrame(1, false, false, []byte{0x40}))
	if _, err := d.ProcessFrame(Frame{Type: FrameRSTStream, StreamID: 1, Payload: []byte{0, 0, 0, 8}}); err != nil {
		t.Fatalf("cancel stream 1: %v", err)
	}
	mustDecode(t, d, continuationFrame(1, false, nil))
	mustDecode(t, d, continuationFrame(1, true, nil))
	_, err = d.ProcessFrame(continuationFrame(1, true, []byte{0x40}))
	if err == nil || !errors.Is(err, ErrUnexpectedContinuation) {
		t.Fatalf("late CONTINUATION error = %v", err)
	}
}

func TestCommitRecordIsReplayable(t *testing.T) {
	d := NewDecoder(Config{MaxDynamicTableSize: 4096})
	block := testTableSizeUpdate(nil, 128)
	value := strings.Repeat("a", 40)
	block = append(block, testLiteralIncremental(nil, "x-a", 0, value, false)...)
	block = append(block, testLiteralIncremental(nil, "x-b", 0, value, false)...)
	block = append(block, testLiteralIncremental(nil, "x-c", 0, value, false)...)
	res := mustDecode(t, d, headersFrame(1, true, false, block))
	if res.Commit == nil {
		t.Fatal("missing commit record")
	}
	if len(res.Commit.Operations) != 4 {
		t.Fatalf("operations = %#v", res.Commit.Operations)
	}
	if len(res.Commit.Evicted) == 0 {
		t.Fatal("expected eviction to be recorded")
	}
	replayTable := NewDynamicTable(res.Commit.InitialMaxSize)
	if err := res.Commit.Replay(replayTable); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(replayTable.Entries(), d.DynamicTable().Entries()) {
		t.Fatalf("replayed table %#v != live %#v", replayTable.Entries(), d.DynamicTable().Entries())
	}
	if res.Commit.TableSizeAfter != replayTable.Size() {
		t.Fatalf("replay size %d, commit size %d", replayTable.Size(), res.Commit.TableSizeAfter)
	}

	// A table can be reduced to zero and later grow again; the commit's
	// allowed maximum must still let the replay apply the larger update.
	assertReplay := func(d *Decoder, frame Frame) TableCommit {
		t.Helper()
		res := mustDecode(t, d, frame)
		if res.Commit == nil {
			t.Fatal("missing commit")
		}
		rt := NewDynamicTable(res.Commit.InitialMaxSize)
		if err := res.Commit.Replay(rt); err != nil {
			t.Fatalf("replay after resize: %v", err)
		}
		if !reflect.DeepEqual(rt.Entries(), d.DynamicTable().Entries()) {
			t.Fatalf("replay after resize: %#v != %#v", rt.Entries(), d.DynamicTable().Entries())
		}
		return *res.Commit
	}
	d = NewDecoder(Config{MaxDynamicTableSize: 4096})
	assertReplay(d, headersFrame(1, true, false, testTableSizeUpdate(nil, 0)))
	regrow := testTableSizeUpdate(nil, 4096)
	regrow = append(regrow, testLiteralIncremental(nil, "x-regrow", 0, "v", false)...)
	commit := assertReplay(d, headersFrame(3, true, false, regrow))
	if commit.InitialMaxSize != 0 || commit.MaxSize != 4096 {
		t.Fatalf("regrow commit sizes: initial=%d max=%d", commit.InitialMaxSize, commit.MaxSize)
	}
	next := testLiteralIncremental(nil, "x-next", 0, "v", false)
	res = mustDecode(t, d, headersFrame(5, true, false, next))
	scratch, err := res.Commit.ReplayFromScratch()
	if err != nil {
		t.Fatalf("replay from scratch: %v", err)
	}
	if !reflect.DeepEqual(scratch.Entries(), d.DynamicTable().Entries()) {
		t.Fatalf("replay from scratch table %#v != live %#v", scratch.Entries(), d.DynamicTable().Entries())
	}
}

func TestFrameWireRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	frames := []Frame{
		headersFrame(1, false, true, []byte{0x40, 0x01}),
		continuationFrame(1, true, []byte{0x02}),
	}
	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range frames {
		got, err := ReadFrame(&buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != want.Type || got.Flags != want.Flags || got.StreamID != want.StreamID ||
			!bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("frame round trip: got %#v, want %#v", got, want)
		}
	}
}

func TestPaddedAndPrioritizedHeadersPayload(t *testing.T) {
	d := NewDecoder(Config{})
	block := testIndexed(nil, 2)              // :method GET
	payload := []byte{3}                      // pad length
	payload = append(payload, 0, 0, 0, 1, 15) // priority dependency and weight
	payload = append(payload, block...)
	payload = append(payload, 0xaa, 0xbb, 0xcc) // padding
	f := Frame{
		Type:     FrameHeaders,
		Flags:    FlagEndHeaders | FlagPadded | FlagPriority,
		StreamID: 1,
		Payload:  payload,
	}
	res := mustDecode(t, d, f)
	if len(res.Fields) != 1 || res.Fields[0].Name != ":method" || res.Fields[0].Value != "GET" {
		t.Fatalf("padded/prioritized HEADERS = %#v", res.Fields)
	}
}
