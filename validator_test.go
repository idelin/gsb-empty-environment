package netsec

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"
)

const (
	credA uint64 = 1001
	credB uint64 = 2002
)

// buildSQE packs an SQE the way the kernel struct is laid out (little endian,
// 64 bytes). spliceFDIn is the int32 union field at offset 40.
func buildSQE(opcode uint8, flags uint8, fd int32, persona uint16) []byte {
	return buildSQEFull(opcode, flags, fd, persona, 0, 0)
}

func buildSQEFull(opcode uint8, flags uint8, fd int32, persona uint16, spliceFDIn int32, upper uint8) []byte {
	b := make([]byte, SQESize)
	b[offOpcode] = opcode
	b[offFlags] = flags
	binary.LittleEndian.PutUint32(b[offFD:], uint32(fd))
	if opcode == OpSplice || opcode == OpTee {
		// splice_fd_in shares a union with personality: write the source
		// slot and leave the personality half-word unreadable by the kernel.
		binary.LittleEndian.PutUint32(b[offSpliceFDIn:], uint32(spliceFDIn))
	} else {
		binary.LittleEndian.PutUint16(b[offPersonality:], persona)
	}
	b[offFileIndexUpper] = upper
	return b
}

func mustParse(t *testing.T, b []byte) *SQE {
	t.Helper()
	e, err := ParseSQE(b)
	if err != nil {
		t.Fatalf("ParseSQE: %v", err)
	}
	return e
}

func newTestTable(t *testing.T) *SlotTable {
	t.Helper()
	tab := NewSlotTable(4)
	credA := Credential{CredID: credA, PersonalityID: 11}
	credB := Credential{CredID: credB, PersonalityID: 22}
	if err := tab.RegisterSlot(0, AccessRead, credA, "read-only-A"); err != nil {
		t.Fatalf("register slot0: %v", err)
	}
	if err := tab.RegisterSlot(1, AccessRead|AccessWrite, credA, "rw-A"); err != nil {
		t.Fatalf("register slot1: %v", err)
	}
	if err := tab.RegisterSlot(2, AccessRead|AccessWrite, credB, "rw-B"); err != nil {
		t.Fatalf("register slot2: %v", err)
	}
	if err := tab.RegisterSlot(3, AccessWrite, credB, "write-only-B"); err != nil {
		t.Fatalf("register slot3: %v", err)
	}
	return tab
}

func assertRule(t *testing.T, err error, want Rule, wantSentinel error) {
	t.Helper()
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RuleError, got %T: %v", err, err)
	}
	if re.Rule != want {
		t.Fatalf("rule = %q, want %q (err: %v)", re.Rule, want, err)
	}
	if wantSentinel != nil && !errors.Is(err, wantSentinel) {
		t.Fatalf("errors.Is(err, %v) = false; got %v", wantSentinel, err)
	}
}

// 1. Out-of-range fixed-file slot must be rejected, including when the
// extended (file_index_upper) encoding is used.
func TestRejectOutOfRangeSlot(t *testing.T) {
	v := New(newTestTable(t), DefaultConfig())

	sqe := mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 4, 11))
	if _, err := v.ValidateOne(sqe); err == nil {
		t.Fatal("slot index 4 in a 4-entry table was accepted")
	} else {
		assertRule(t, err, RuleSlotRange, ErrSlotOutOfRange)
	}

	// Extended 40-bit index: upper byte 1 -> index 1<<32.
	ext := mustParse(t, buildSQEFull(OpRead, IOSQEFixedFile|IOSQEFileIndexUpper, 0, 11, 0, 1))
	if got := ext.FixedIndex(); got != 1<<32 {
		t.Fatalf("FixedIndex = %d, want %d", got, uint64(1)<<32)
	}
	if _, err := v.ValidateOne(ext); err == nil {
		t.Fatal("extended index beyond table size was accepted")
	} else {
		assertRule(t, err, RuleSlotRange, ErrSlotOutOfRange)
	}

	// Negative index encoding (fd = -1) wraps to a huge unsigned index.
	neg := mustParse(t, buildSQE(OpRead, IOSQEFixedFile, -1, 11))
	if _, err := v.ValidateOne(neg); err == nil {
		t.Fatal("negative fixed-file index was accepted")
	} else {
		assertRule(t, err, RuleSlotRange, ErrSlotOutOfRange)
	}
}

// 2. A read-only slot must refuse write-class opcodes while still serving
// reads.
func TestRejectWriteOnReadOnlySlot(t *testing.T) {
	v := New(newTestTable(t), DefaultConfig())

	writeSQE := mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 0, 11))
	if _, err := v.ValidateOne(writeSQE); err == nil {
		t.Fatal("WRITE on read-only slot 0 was accepted")
	} else {
		assertRule(t, err, RuleSlotAccess, ErrSlotAccessDenied)
	}

	// Same slot, read-class opcode: allowed.
	readSQE := mustParse(t, buildSQE(OpRead, IOSQEFixedFile, 0, 11))
	dec, err := v.ValidateOne(readSQE)
	if err != nil {
		t.Fatalf("READ on read-only slot 0 rejected: %v", err)
	}
	if len(dec.Slots) != 1 || dec.Slots[0] != 0 || dec.CredID != credA {
		t.Fatalf("unexpected decision: %+v", dec)
	}

	// Write-only slot symmetrically refuses reads.
	ro := mustParse(t, buildSQE(OpRecv, IOSQEFixedFile, 3, 22))
	if _, err := v.ValidateOne(ro); err == nil {
		t.Fatal("RECV on write-only slot 3 was accepted")
	} else {
		assertRule(t, err, RuleSlotAccess, ErrSlotAccessDenied)
	}
}

// 3. A linked chain may not hop from a slot owned by credential A to a slot
// owned by credential B; same-credential chains pass.
func TestRejectChainSwitchingSlots(t *testing.T) {
	v := New(newTestTable(t), DefaultConfig())

	// Isolate the credential rule: disable personality checks so differing
	// personas cannot mask the cross-credential violation.
	cfg := DefaultConfig()
	cfg.CheckPersonality = false
	credV := New(newTestTable(t), cfg)

	// [WRITE slot1 (credA) -> READ slot2 (credB)] linked: must fail.
	bad := []*SQE{
		mustParse(t, buildSQE(OpWrite, IOSQEFixedFile|IOSQEIOLink, 1, 11)),
		mustParse(t, buildSQE(OpRead, IOSQEFixedFile, 2, 22)),
	}
	if _, err := credV.ValidateBatch(bad); err == nil {
		t.Fatal("chain hopping between credentials was accepted")
	} else {
		assertRule(t, err, RuleChainCred, ErrChainCredMismatch)
	}

	// Same credential, both slots belong to credA: accepted.
	good := []*SQE{
		mustParse(t, buildSQE(OpRead, IOSQEFixedFile|IOSQEHARDLink, 0, 11)),
		mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 1, 11)),
	}
	decs, err := v.ValidateBatch(good)
	if err != nil {
		t.Fatalf("same-credential chain rejected: %v", err)
	}
	if len(decs) != 2 || decs[0].CredID != credA || decs[1].CredID != credA {
		t.Fatalf("unexpected decisions: %+v", decs)
	}

	// Same credential but two registered personas: each entry is legal on
	// its own, yet the chain must present one consistent personality.
	personaTab := NewSlotTable(2)
	if err := personaTab.RegisterSlot(0, AccessRead, Credential{CredID: credA, PersonalityID: 11}, "p11"); err != nil {
		t.Fatalf("register slot0: %v", err)
	}
	if err := personaTab.RegisterSlot(1, AccessWrite, Credential{CredID: credA, PersonalityID: 22}, "p22"); err != nil {
		t.Fatalf("register slot1: %v", err)
	}
	personaV := New(personaTab, DefaultConfig())
	personaSwap := []*SQE{
		mustParse(t, buildSQE(OpRead, IOSQEFixedFile|IOSQEIOLink, 0, 11)),
		mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 1, 22)),
	}
	if _, err := personaV.ValidateBatch(personaSwap); err == nil {
		t.Fatal("chain with swapped personality was accepted")
	} else {
		assertRule(t, err, RuleChainPersonality, ErrChainPersMismatch)
	}

	// Link flag on the final element: dangling link.
	dangling := []*SQE{
		mustParse(t, buildSQE(OpRead, IOSQEFixedFile|IOSQEIOLink, 0, 11)),
	}
	if _, err := v.ValidateBatch(dangling); err == nil {
		t.Fatal("dangling link was accepted")
	} else {
		assertRule(t, err, RuleChainLink, ErrDanglingLink)
	}
}

// 4. Once a slot is revoked, reuse of the same slot must stay blocked even
// though the kernel fixed-file reference may still be alive.
func TestRejectReuseAfterRevocation(t *testing.T) {
	tab := newTestTable(t)
	v := New(tab, DefaultConfig())

	sqe := mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 1, 11))
	if _, err := v.ValidateOne(sqe); err != nil {
		t.Fatalf("slot 1 usable before revocation: %v", err)
	}

	if err := tab.Revoke(1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := v.ValidateOne(sqe); err == nil {
		t.Fatal("revoked slot 1 was still accepted")
	} else {
		assertRule(t, err, RuleSlotState, ErrSlotNotActive)
	}

	// Revocation must be sticky across repeated submissions.
	for i := 0; i < 3; i++ {
		if _, err := v.ValidateOne(sqe); err == nil {
			t.Fatalf("revoked slot accepted on attempt %d", i+2)
		}
	}

	// A fresh registration (new generation) reopens the slot.
	credA := Credential{CredID: credA, PersonalityID: 11}
	if err := tab.RegisterSlot(1, AccessRead|AccessWrite, credA, "rw-A-reborn"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if _, err := v.ValidateOne(sqe); err != nil {
		t.Fatalf("freshly registered slot rejected: %v", err)
	}
}

func TestOpcodeWhitelistAndFixedFileRules(t *testing.T) {
	v := New(newTestTable(t), DefaultConfig())

	// Unknown opcode (200) is never whitelisted.
	if _, err := v.ValidateOne(mustParse(t, buildSQE(200, 0, 0, 0))); err == nil {
		t.Fatal("unknown opcode accepted")
	} else {
		assertRule(t, err, RuleOpcodeWhitelist, ErrOpcodeNotAllowed)
	}

	// Known but non-default opcode (OPENAT opens new fds, excluded).
	openat := mustParse(t, buildSQE(OpOpenat, IOSQEFixedFile, 1, 11))
	if _, err := v.ValidateOne(openat); err == nil {
		t.Fatal("non-whitelisted OPENAT accepted")
	} else {
		assertRule(t, err, RuleOpcodeWhitelist, ErrOpcodeNotAllowed)
	}

	// fd-class opcode without IOSQE_FIXED_FILE under RequireFixedFile.
	raw := mustParse(t, buildSQE(OpRead, 0, 1, 11))
	if _, err := v.ValidateOne(raw); err == nil {
		t.Fatal("raw fd read accepted while fixed files are required")
	} else {
		assertRule(t, err, RuleFixedFileRequired, ErrFixedFileRequired)
	}

	// Fixed-file flag on a no-fd opcode (NOP).
	nop := mustParse(t, buildSQE(OpNop, IOSQEFixedFile, 0, 0))
	if _, err := v.ValidateOne(nop); err == nil {
		t.Fatal("NOP with fixed-file flag accepted")
	} else {
		assertRule(t, err, RuleFixedFileForbidden, ErrFixedFileForbidden)
	}
}

func TestPersonalityMismatch(t *testing.T) {
	v := New(newTestTable(t), DefaultConfig())

	// Slot 1 belongs to persona 11; SQE presents persona 22.
	sqe := mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 1, 22))
	if _, err := v.ValidateOne(sqe); err == nil {
		t.Fatal("personality mismatch accepted")
	} else {
		assertRule(t, err, RulePersonality, ErrPersonality)
	}
}

func TestSpliceValidatesBothSlots(t *testing.T) {
	tab := newTestTable(t)
	v := New(tab, DefaultConfig())

	// SPLICE: out slot 1 (credA, writable), in slot 0 (credA, readable).
	ok := mustParse(t, buildSQEFull(OpSplice, IOSQEFixedFile, 1, 11, 0, 0))
	dec, err := v.ValidateOne(ok)
	if err != nil {
		t.Fatalf("same-cred splice rejected: %v", err)
	}
	if len(dec.Slots) != 2 || dec.Slots[0] != 1 || dec.Slots[1] != 0 {
		t.Fatalf("unexpected splice decision: %+v", dec)
	}

	// In-slot belongs to credential B: cross-credential splice rejected.
	cross := mustParse(t, buildSQEFull(OpSplice, IOSQEFixedFile, 1, 11, 2, 0))
	if _, err := v.ValidateOne(cross); err == nil {
		t.Fatal("splice mixing credentials accepted")
	} else {
		assertRule(t, err, RuleChainCred, ErrChainCredMismatch)
	}

	// Read-only out slot cannot be the splice destination.
	roOut := mustParse(t, buildSQEFull(OpSplice, IOSQEFixedFile, 0, 11, 1, 0))
	if _, err := v.ValidateOne(roOut); err == nil {
		t.Fatal("splice into read-only slot accepted")
	} else {
		assertRule(t, err, RuleSlotAccess, ErrSlotAccessDenied)
	}

	// Negative splice_fd_in is never a legal fixed-file index.
	badIn := mustParse(t, buildSQEFull(OpTee, IOSQEFixedFile, 1, 11, -1, 0))
	if _, err := v.ValidateOne(badIn); err == nil {
		t.Fatal("negative splice_fd_in accepted")
	} else {
		assertRule(t, err, RuleSlotRange, ErrSlotOutOfRange)
	}
}

func TestFlagAndIndexEncoding(t *testing.T) {
	// Stricter config only allows the fixed-file and link bits.
	strict := New(newTestTable(t), Config{
		AllowedOpcodes:   DefaultOpcodes(),
		AllowedFlags:     IOSQEFixedFile | IOSQEIOLink,
		RequireFixedFile: true,
		CheckPersonality: true,
	})

	drain := mustParse(t, buildSQE(OpRead, IOSQEFixedFile|IOSQEIODrain, 1, 11))
	if _, err := strict.ValidateOne(drain); err == nil {
		t.Fatal("disallowed IOSQE_IO_DRAIN flag accepted")
	} else {
		assertRule(t, err, RuleFlagAllowed, ErrFlagNotAllowed)
	}

	v := New(newTestTable(t), DefaultConfig())

	// High byte set without the FILE_INDEX_UPPER flag: forged encoding.
	forged := mustParse(t, buildSQEFull(OpRead, IOSQEFixedFile, 0, 11, 0, 1))
	if _, err := v.ValidateOne(forged); err == nil {
		t.Fatal("non-zero file_index_upper without its flag was accepted")
	} else {
		assertRule(t, err, RuleIndexEncoding, ErrIndexEncoding)
	}

	// Extended index that resolves into the table decodes correctly.
	extended := mustParse(t, buildSQEFull(OpRead, IOSQEFixedFile|IOSQEFileIndexUpper, 2, 22, 0, 0))
	if extended.FixedIndex() != 2 {
		t.Fatalf("FixedIndex = %d, want 2", extended.FixedIndex())
	}
	if _, err := v.ValidateOne(extended); err != nil {
		t.Fatalf("valid extended-index SQE rejected: %v", err)
	}
}

func TestParseAndBatchBasics(t *testing.T) {
	if _, err := ParseSQE(make([]byte, 63)); err == nil {
		t.Fatal("63-byte SQE accepted")
	} else {
		assertRule(t, err, RuleSQESize, ErrSQESize)
	}

	v := New(newTestTable(t), DefaultConfig())
	if _, err := v.ValidateBatch(nil); err == nil {
		t.Fatal("empty batch accepted")
	} else {
		assertRule(t, err, RuleEmptyBatch, ErrEmptyBatch)
	}

	// Two independent (unlinked) SQEs in one batch both validate.
	batch := []*SQE{
		mustParse(t, buildSQE(OpRead, IOSQEFixedFile, 0, 11)),
		mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 2, 22)),
	}
	decs, err := v.ValidateBatch(batch)
	if err != nil {
		t.Fatalf("independent batch rejected: %v", err)
	}
	if decs[0].CredID != credA || decs[1].CredID != credB {
		t.Fatalf("unexpected batch decisions: %+v", decs)
	}
}

// Revocation racing a batch must not split a chain: the snapshot is taken
// once, so either every member passes or every member is rejected together.
func TestBatchUsesSingleSnapshot(t *testing.T) {
	tab := newTestTable(t)
	v := New(tab, DefaultConfig())

	chain := []*SQE{
		mustParse(t, buildSQE(OpRead, IOSQEFixedFile|IOSQEIOLink, 0, 11)),
		mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 1, 11)),
	}
	if _, err := v.ValidateBatch(chain); err != nil {
		t.Fatalf("chain before revocation rejected: %v", err)
	}

	if err := tab.Revoke(0); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := v.ValidateBatch(chain); err == nil {
		t.Fatal("chain using revoked slot was accepted")
	} else {
		assertRule(t, err, RuleSlotState, ErrSlotNotActive)
	}
}

// Concurrent revocation/registration against validation must never race or
// return a decision built from a half-mutated slot.
func TestConcurrentValidateAndRevoke(t *testing.T) {
	tab := newTestTable(t)
	v := New(tab, DefaultConfig())
	sqe := mustParse(t, buildSQE(OpWrite, IOSQEFixedFile, 1, 11))

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				if _, err := v.ValidateOne(sqe); err != nil {
					assertRuleOnly(t, err)
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		credA := Credential{CredID: credA, PersonalityID: 11}
		for n := 0; n < 200; n++ {
			_ = tab.Revoke(1)
			_ = tab.RegisterSlot(1, AccessRead|AccessWrite, credA, "rw-A")
		}
	}()
	wg.Wait()
}

func assertRuleOnly(t *testing.T, err error) {
	t.Helper()
	var re *RuleError
	if !errors.As(err, &re) {
		t.Errorf("expected *RuleError, got %T: %v", err, err)
	}
}
