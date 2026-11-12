package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"testing"
)

// refExtend is an independent re-implementation of the extension
// construction used only by the tests to cross-check Extend.
func refExtend(prev []byte, index uint32, etype EventType, data []byte) []byte {
	digest := sha256.New()
	digest.Write(prev)
	var head [18]byte
	binary.BigEndian.PutUint64(head[0:8], uint64(index))
	binary.BigEndian.PutUint16(head[8:10], uint16(etype))
	binary.BigEndian.PutUint64(head[10:18], uint64(len(data)))
	digest.Write(head[:])
	digest.Write(data)
	return digest.Sum(nil)
}

type simulator struct {
	regs [][]byte
	zero []byte
}

func newSimulator(numRegisters int) *simulator {
	sim := &simulator{zero: make([]byte, sha256.Size)}
	for i := 0; i < numRegisters; i++ {
		sim.regs = append(sim.regs, append([]byte(nil), sim.zero...))
	}
	return sim
}

func (s *simulator) apply(entry Entry) {
	if entry.Type == EventStartup {
		s.regs[entry.Register] = append([]byte(nil), s.zero...)
	}
	s.regs[entry.Register] = refExtend(s.regs[entry.Register], entry.Register, entry.Type, entry.Data)
}

func (s *simulator) snapshot() [][]byte {
	out := make([][]byte, len(s.regs))
	for i, value := range s.regs {
		out[i] = append([]byte(nil), value...)
	}
	return out
}

func buildLog() (log []Entry, final [][]byte, quote map[uint32][]byte) {
	const numRegisters = 4
	sim := newSimulator(numRegisters)
	events := []Entry{
		{Register: 0, Type: EventBoot, Data: []byte("firmware")},
		{Register: 1, Type: EventConfig, Data: []byte("kernel cmdline")},
		{Register: 0, Type: EventConfig, Data: []byte("kernel")},
		{Register: 2, Type: EventBoot, Data: []byte("bootloader-stage2")},
		{Register: 3, Type: EventApp, Data: []byte("workload-a")},
		{Register: 1, Type: EventApp, Data: []byte("policy")},
		{Register: 0, Type: EventStartup, Data: []byte("relaunch")},
		{Register: 0, Type: EventBoot, Data: []byte("firmware-2")},
	}
	log = make([]Entry, 0, len(events))
	for _, entry := range events {
		sim.apply(entry)
		entry.State = sim.snapshot()
		log = append(log, entry)
	}
	quote = map[uint32][]byte{}
	for i, value := range sim.regs {
		quote[uint32(i)] = append([]byte(nil), value...)
	}
	return log, sim.regs, quote
}

func stripState(log []Entry) []Entry {
	out := make([]Entry, len(log))
	copy(out, log)
	for i := range out {
		out[i].State = nil
	}
	return out
}

func cloneLog(log []Entry) []Entry {
	out := make([]Entry, len(log))
	copy(out, log)
	for i := range out {
		out[i].Data = append([]byte(nil), log[i].Data...)
		if log[i].State != nil {
			out[i].State = make([][]byte, len(log[i].State))
			for j, value := range log[i].State {
				out[i].State[j] = append([]byte(nil), value...)
			}
		}
	}
	return out
}

func TestReplayHonestLog(t *testing.T) {
	log, final, quote := buildLog()
	result, err := Replay(log, Config{Quote: quote})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if !result.OK() {
		t.Fatalf("honest log rejected: %+v", result.Mismatch)
	}
	if result.Prefix != len(log) {
		t.Fatalf("Prefix = %d, want %d", result.Prefix, len(log))
	}
	for i, value := range final {
		if !bytes.Equal(result.Final[i], value) {
			t.Fatalf("final register %d = %x, want %x", i, result.Final[i], value)
		}
	}
}

func TestRegistersEvolveIndependently(t *testing.T) {
	log, _, _ := buildLog()
	result, err := Replay(log[:2], Config{})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	want0 := refExtend(make([]byte, sha256.Size), 0, EventBoot, []byte("firmware"))
	want1 := refExtend(make([]byte, sha256.Size), 1, EventConfig, []byte("kernel cmdline"))
	if !bytes.Equal(result.Final[0], want0) {
		t.Fatalf("register 0 drifted: %x", result.Final[0])
	}
	if !bytes.Equal(result.Final[1], want1) {
		t.Fatalf("register 1 drifted: %x", result.Final[1])
	}
	for i := 2; i < len(result.Final); i++ {
		if !bytes.Equal(result.Final[i], make([]byte, sha256.Size)) {
			t.Fatalf("untouched register %d is non-zero: %x", i, result.Final[i])
		}
	}
}

func TestStartupResetIsAuthorizedAndMeasured(t *testing.T) {
	log, _, quote := buildLog()
	result, err := Replay(log, Config{Quote: quote})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	zero := make([]byte, sha256.Size)
	// Entry 6 is the authorized startup event on register 0: its value
	// must be the extension of zero, proving the reset happened and that
	// the launch event itself is still measured.
	wantAfterStartup := refExtend(zero, 0, EventStartup, []byte("relaunch"))
	if !bytes.Equal(log[6].State[0], wantAfterStartup) {
		t.Fatalf("startup did not reset-then-extend: %x", log[6].State[0])
	}
	wantFinal := refExtend(wantAfterStartup, 0, EventBoot, []byte("firmware-2"))
	if !bytes.Equal(result.Final[0], wantFinal) {
		t.Fatalf("post-startup chain wrong: %x", result.Final[0])
	}
}

func TestResetWithoutStartupEventIsRejected(t *testing.T) {
	zero := make([]byte, sha256.Size)
	first := Entry{Register: 0, Type: EventBoot, Data: []byte("a")}
	second := Entry{Register: 0, Type: EventBoot, Data: []byte("b")}
	sim := newSimulator(1)
	sim.apply(first)
	sim.apply(second)

	// Claim register 0 had been silently reset before "b": the honest
	// replay keeps the chain and must reject the claimed state.
	forged := second
	forged.State = [][]byte{refExtend(zero, 0, EventBoot, []byte("b"))}
	log := []Entry{first, forged}
	result, err := Replay(log, Config{NumRegisters: 1})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() || result.Mismatch == nil {
		t.Fatal("unauthorized reset was accepted")
	}
	if result.Mismatch.Entry != 1 || result.Mismatch.Register != 0 || result.Mismatch.Kind != "entry-state" {
		t.Fatalf("unexpected mismatch: %+v", result.Mismatch)
	}
	if result.Prefix != 1 {
		t.Fatalf("Prefix = %d, want 1", result.Prefix)
	}
	if !bytes.Equal(result.Final[0], sim.regs[0]) {
		t.Fatalf("final value not the chained value: %x", result.Final[0])
	}
}

func TestTruncatedLogFailsQuote(t *testing.T) {
	log, _, quote := buildLog()
	short := log[:4]
	result, err := Replay(short, Config{Quote: quote})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() {
		t.Fatal("truncated log verified against the full quote")
	}
	if result.Mismatch.Kind != "quote" || result.Mismatch.Entry != -1 {
		t.Fatalf("expected quote mismatch, got %+v", result.Mismatch)
	}
	if result.Prefix != len(short) {
		t.Fatalf("Prefix = %d, want %d", result.Prefix, len(short))
	}

	// Without a quote the honest prefix still validates per-entry state.
	prefixOnly, err := Replay(short, Config{})
	if err != nil || !prefixOnly.OK() {
		t.Fatalf("honest prefix rejected: err=%v mismatch=%+v", err, prefixOnly.Mismatch)
	}
}

func TestEventDataTampering(t *testing.T) {
	log, _, quote := buildLog()
	tampered := cloneLog(log)
	tampered[3].Data[0] ^= 0xFF
	result, err := Replay(tampered, Config{Quote: quote})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() {
		t.Fatal("tampered event data was accepted")
	}
	if result.Mismatch.Entry != 3 || result.Mismatch.Kind != "entry-state" {
		t.Fatalf("expected mismatch at entry 3, got %+v", result.Mismatch)
	}
	if result.Prefix != 3 {
		t.Fatalf("Prefix = %d, want 3", result.Prefix)
	}

	// The full log is still replayed, so Final must equal an independent
	// simulation of the tampered log.
	sim := newSimulator(4)
	for _, entry := range tampered {
		sim.apply(entry)
	}
	for i := range sim.regs {
		if !bytes.Equal(result.Final[i], sim.regs[i]) {
			t.Fatalf("final register %d not fully replayed", i)
		}
	}
}

func TestEventTypeTampering(t *testing.T) {
	log, _, _ := buildLog()
	tampered := cloneLog(log)
	tampered[2].Type = EventApp
	result, err := Replay(tampered, Config{})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() || result.Mismatch.Entry != 2 {
		t.Fatalf("retyped event was accepted: %+v", result.Mismatch)
	}
}

func TestEventDeletion(t *testing.T) {
	log, _, quote := buildLog()
	cut := make([]Entry, 0, len(log)-1)
	cut = append(cut, log[:2]...)
	cut = append(cut, log[3:]...)
	result, err := Replay(cut, Config{Quote: quote})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() {
		t.Fatal("log with a deleted event was accepted")
	}
	if result.Mismatch.Entry != 2 {
		t.Fatalf("expected first mismatch at entry 2, got %+v", result.Mismatch)
	}
	if result.Prefix != 2 {
		t.Fatalf("Prefix = %d, want 2", result.Prefix)
	}
}

func TestCrossRegisterDiversionWithState(t *testing.T) {
	log, _, _ := buildLog()
	diverted := cloneLog(log)
	// Entry 1 originally extends register 1; redirect it at register 0
	// while keeping the originally signed per-entry state.
	diverted[1].Register = 0
	result, err := Replay(diverted, Config{})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() {
		t.Fatal("cross-register diversion was accepted")
	}
	if result.Mismatch.Entry != 1 || result.Mismatch.Kind != "entry-state" {
		t.Fatalf("expected entry-state mismatch at entry 1, got %+v", result.Mismatch)
	}
}

func TestCrossRegisterDiversionAgainstQuote(t *testing.T) {
	log, _, quote := buildLog()
	// No per-entry state: the attacker can only be caught by the quote.
	// Swap the target registers of two entries while leaving data intact.
	diverted := stripState(log)
	diverted[1].Register, diverted[3].Register = diverted[3].Register, diverted[1].Register
	result, err := Replay(diverted, Config{Quote: quote})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.OK() {
		t.Fatal("cross-register diversion matched the original quote")
	}
	if result.Mismatch.Kind != "quote" || result.Mismatch.Entry != -1 {
		t.Fatalf("expected quote mismatch, got %+v", result.Mismatch)
	}
}

func TestExtensionBindsLengthAndTypeAndIndex(t *testing.T) {
	zero := make([]byte, sha256.Size)
	short := Extend(sha256.New, zero, 0, EventBoot, []byte{'a'})
	padded := Extend(sha256.New, zero, 0, EventBoot, []byte{'a', 0x00})
	if bytes.Equal(short, padded) {
		t.Fatal("length is not bound into the digest")
	}
	typed := Extend(sha256.New, zero, 0, EventConfig, []byte{'a'})
	if bytes.Equal(short, typed) {
		t.Fatal("event type is not bound into the digest")
	}
	indexed := Extend(sha256.New, zero, 1, EventBoot, []byte{'a'})
	if bytes.Equal(short, indexed) {
		t.Fatal("register index is not bound into the digest")
	}
	// Cross-check the exact byte layout against the independent reference.
	want := refExtend(zero, 7, EventApp, []byte("payload"))
	got := Extend(sha256.New, zero, 7, EventApp, []byte("payload"))
	if !bytes.Equal(got, want) {
		t.Fatalf("Extend layout diverges from the specification: %x != %x", got, want)
	}
}

func TestMalformedLogAndQuote(t *testing.T) {
	log, _, quote := buildLog()

	badRegister := cloneLog(log)
	badRegister[0].Register = 99
	if _, err := Replay(badRegister, Config{}); !errors.Is(err, ErrBadRegister) {
		t.Fatalf("unknown register: got %v, want ErrBadRegister", err)
	}

	badType := cloneLog(log)
	badType[0].Type = EventType(0x0101)
	if _, err := Replay(badType, Config{}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown type: got %v, want ErrUnknownType", err)
	}

	badStateLen := cloneLog(log)
	badStateLen[0].State = make([][]byte, 3)
	if _, err := Replay(badStateLen, Config{}); !errors.Is(err, ErrStateShape) {
		t.Fatalf("short state vector: got %v, want ErrStateShape", err)
	}

	badStateDigest := cloneLog(log)
	badStateDigest[0].State[0] = make([]byte, sha256.Size-1)
	if _, err := Replay(badStateDigest, Config{}); !errors.Is(err, ErrDigestSize) {
		t.Fatalf("short digest: got %v, want ErrDigestSize", err)
	}

	incompleteQuote := map[uint32][]byte{}
	for i, value := range quote {
		if i == 2 {
			continue
		}
		incompleteQuote[i] = value
	}
	if _, err := Replay(stripState(log), Config{Quote: incompleteQuote}); !errors.Is(err, ErrIncompleteQuote) {
		t.Fatalf("incomplete quote: got %v, want ErrIncompleteQuote", err)
	}

	wrongSizedQuote := map[uint32][]byte{}
	for i, value := range quote {
		wrongSizedQuote[i] = append([]byte(nil), value...)
	}
	wrongSizedQuote[1] = make([]byte, 7)
	if _, err := Replay(stripState(log), Config{Quote: wrongSizedQuote}); !errors.Is(err, ErrDigestSize) {
		t.Fatalf("wrong-sized quote: got %v, want ErrDigestSize", err)
	}
}

func TestReplayWithCustomHash(t *testing.T) {
	cfg := Config{NewHash: sha512.New, NumRegisters: 2}
	first := Entry{Register: 0, Type: EventBoot, Data: []byte("a")}
	second := Entry{Register: 1, Type: EventConfig, Data: []byte("b")}
	result, err := Replay([]Entry{first, second}, cfg)
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	zero := make([]byte, sha512.Size)
	want0 := Extend(sha512.New, zero, 0, EventBoot, []byte("a"))
	want1 := Extend(sha512.New, zero, 1, EventConfig, []byte("b"))
	if !bytes.Equal(result.Final[0], want0) || !bytes.Equal(result.Final[1], want1) {
		t.Fatalf("SHA-512 replay produced wrong registers: %x %x", result.Final[0], result.Final[1])
	}
	if !result.OK() {
		t.Fatalf("hash-only replay reported a mismatch: %+v", result.Mismatch)
	}
}

func TestConfigurableStartupTypes(t *testing.T) {
	// Authorize EventConfig as a reset type on register 1 only.
	cfg := Config{
		NumRegisters: 2,
		StartupTypes: []EventType{EventConfig},
	}
	zero := make([]byte, sha256.Size)
	log := []Entry{
		{Register: 1, Type: EventBoot, Data: []byte("warmup")},
		{Register: 1, Type: EventConfig, Data: []byte("reboot-cfg")},
	}
	result, err := Replay(log, cfg)
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	want := refExtend(zero, 1, EventConfig, []byte("reboot-cfg"))
	if !bytes.Equal(result.Final[1], want) {
		t.Fatalf("authorized custom reset not applied: %x", result.Final[1])
	}

	// Under the default policy EventConfig must not reset anything.
	defaultResult, err := Replay(log, Config{NumRegisters: 2})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	chained := refExtend(refExtend(zero, 1, EventBoot, []byte("warmup")), 1, EventConfig, []byte("reboot-cfg"))
	if !bytes.Equal(defaultResult.Final[1], chained) {
		t.Fatalf("default policy unexpectedly reset register 1: %x", defaultResult.Final[1])
	}
}

func TestFirstMismatchPrefixIsStable(t *testing.T) {
	log, _, _ := buildLog()
	tampered := cloneLog(log)
	tampered[2].Data[0] ^= 0x01
	tampered[5].Data[0] ^= 0x02
	result, err := Replay(tampered, Config{})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if result.Mismatch == nil || result.Mismatch.Entry != 2 {
		t.Fatalf("expected first mismatch at entry 2, got %+v", result.Mismatch)
	}
	if result.Prefix != 2 {
		t.Fatalf("Prefix = %d, want 2", result.Prefix)
	}
	// Every entry strictly before the mismatch still validates.
	for i := 0; i < result.Prefix; i++ {
		for regIndex, claimed := range tampered[i].State {
			if !bytes.Equal(claimed, log[i].State[regIndex]) {
				t.Fatalf("prefix entry %d unexpectedly altered", i)
			}
		}
	}
}
