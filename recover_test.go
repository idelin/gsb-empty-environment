package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeLog(t *testing.T, path string, events ...Event) []byte {
	t.Helper()
	l, err := OpenLogger(path)
	if err != nil {
		t.Fatalf("OpenLogger: %v", err)
	}
	for i, e := range events {
		if _, err := l.Append(e); err != nil {
			t.Fatalf("Append event %d: %v", i, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return data
}

// recordEnds returns the end offset of every complete record in data.
func recordEnds(t *testing.T, data []byte) []int {
	t.Helper()
	var ends []int
	off := 0
	for off < len(data) {
		_, _, n, err := decodeRecordAt(data, off)
		if err != nil {
			t.Fatalf("decodeRecordAt(%d): %v", off, err)
		}
		off += n
		ends = append(ends, off)
	}
	return ends
}

func grantsOf(st *State) map[string][]string {
	out := make(map[string][]string)
	for subj := range st.grants {
		out[subj] = st.Permissions(subj)
	}
	return out
}

// TestPowerFailureEveryByte simulates a power cut after every single byte
// of the log. Recovery must always succeed by truncating the torn,
// uncommitted suffix, and the recovered permissions must equal exactly
// those whose commit record was fully written — never a partially
// written upgrade "completed" into a success.
func TestPowerFailureEveryByte(t *testing.T) {
	dir := t.TempDir()
	events := []Event{
		{EventProofReceived, "alice", "firmware:write", "proof-1"},
		{EventVerified, "alice", "firmware:write", "proof-1"},
		{EventCommit, "alice", "firmware:write", "proof-1"},
		{EventProofReceived, "bob", "net:admin", "proof-2"},
		{EventVerified, "bob", "net:admin", "proof-2"},
		{EventCommit, "bob", "net:admin", "proof-2"},
		{EventProofReceived, "alice", "keys:rotate", "proof-3"},
		{EventVerified, "alice", "keys:rotate", "proof-3"},
		{EventCommit, "alice", "keys:rotate", "proof-3"},
	}
	full := writeLog(t, filepath.Join(dir, "full.log"), events...)
	ends := recordEnds(t, full)

	// Expected state for a crash after byte i: replay only the events
	// whose records end at or before i.
	expectedAt := func(i int) (map[string][]string, int) {
		st := newState()
		trunc := 0
		for j, e := range events {
			if ends[j] > i {
				break
			}
			st.apply(e)
			trunc = ends[j]
		}
		return grantsOf(st), trunc
	}

	for i := 0; i <= len(full); i++ {
		path := filepath.Join(dir, fmt.Sprintf("crash-%d.log", i))
		if err := os.WriteFile(path, full[:i], 0o600); err != nil {
			t.Fatalf("write crash image %d: %v", i, err)
		}
		st, err := Recover(path)
		if err != nil {
			t.Fatalf("prefix %d: Recover: %v", i, err)
		}
		want, wantSize := expectedAt(i)
		if got := grantsOf(st); !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix %d: grants = %v, want %v", i, got, want)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("prefix %d: stat: %v", i, err)
		}
		if fi.Size() != int64(wantSize) {
			t.Fatalf("prefix %d: truncated size = %d, want %d", i, fi.Size(), wantSize)
		}
	}
}

// TestMidLogCorruptionRefused flips one byte inside an early record while
// later records stay intact. Recovery must refuse rather than skip the
// damage and replay whatever follows.
func TestMidLogCorruptionRefused(t *testing.T) {
	dir := t.TempDir()
	events := []Event{
		{EventProofReceived, "alice", "firmware:write", "proof-1"},
		{EventVerified, "alice", "firmware:write", "proof-1"},
		{EventCommit, "alice", "firmware:write", "proof-1"},
		{EventProofReceived, "bob", "net:admin", "proof-2"},
		{EventVerified, "bob", "net:admin", "proof-2"},
		{EventCommit, "bob", "net:admin", "proof-2"},
	}
	full := writeLog(t, filepath.Join(dir, "full.log"), events...)
	ends := recordEnds(t, full)

	corruptOffsets := []int{
		0,                       // magic of first record
		ends[0] - 1,             // crc of first record
		ends[0] + recordHdrSize, // payload of second record
		ends[2] + 4,             // seq of fourth record
	}
	for _, off := range corruptOffsets {
		path := filepath.Join(dir, fmt.Sprintf("corrupt-%d.log", off))
		img := append([]byte(nil), full...)
		img[off] ^= 0xFF
		if err := os.WriteFile(path, img, 0o600); err != nil {
			t.Fatalf("write corrupt image: %v", err)
		}
		st, err := Recover(path)
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt at %d: err = %v, want ErrCorrupt", off, err)
		}
		if st != nil {
			t.Fatalf("corrupt at %d: state = %v, want nil", off, st)
		}
		fi, serr := os.Stat(path)
		if serr != nil {
			t.Fatalf("corrupt at %d: stat: %v", off, serr)
		}
		if fi.Size() != int64(len(full)) {
			t.Fatalf("corrupt at %d: file modified, size = %d, want %d", off, fi.Size(), len(full))
		}
	}
}

// TestRevokeThenRestart ensures a revoked permission never reappears
// after a restart, even when a torn re-commit is appended afterwards.
func TestRevokeThenRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device.log")
	events := []Event{
		{EventProofReceived, "alice", "firmware:write", "proof-1"},
		{EventVerified, "alice", "firmware:write", "proof-1"},
		{EventCommit, "alice", "firmware:write", "proof-1"},
		{EventRevoke, "alice", "firmware:write", ""},
	}
	full := writeLog(t, path, events...)

	st, err := Recover(path)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if st.Allows("alice", "firmware:write") {
		t.Fatal("revoked permission survived restart")
	}

	// Power loss halfway through a new commit for the same permission:
	// the torn record must be dropped, not completed into a grant.
	torn, err := encodeRecord(4, Event{EventCommit, "alice", "firmware:write", "proof-1"})
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(torn[:len(torn)/2]); err != nil {
		t.Fatalf("write torn commit: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st, err = Recover(path)
	if err != nil {
		t.Fatalf("Recover after torn commit: %v", err)
	}
	if st.Allows("alice", "firmware:write") {
		t.Fatal("torn commit restored a revoked permission")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != int64(len(full)) {
		t.Fatalf("torn suffix not truncated: size = %d, want %d", fi.Size(), len(full))
	}
}

// TestUnprovenCommitRejected verifies that a commit record alone never
// restores a permission: the matching proof must have been received and
// verified first.
func TestUnprovenCommitRejected(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]Event{
		"commit-only": {
			{EventCommit, "mallory", "root:shell", "proof-x"},
		},
		"proof-no-verify": {
			{EventProofReceived, "mallory", "root:shell", "proof-x"},
			{EventCommit, "mallory", "root:shell", "proof-x"},
		},
		"wrong-proof-id": {
			{EventProofReceived, "mallory", "root:shell", "proof-x"},
			{EventVerified, "mallory", "root:shell", "proof-x"},
			{EventCommit, "mallory", "root:shell", "proof-y"},
		},
		"verify-without-proof": {
			{EventVerified, "mallory", "root:shell", "proof-x"},
			{EventCommit, "mallory", "root:shell", "proof-x"},
		},
	}
	for name, events := range cases {
		path := filepath.Join(dir, name+".log")
		writeLog(t, path, events...)
		st, err := Recover(path)
		if err != nil {
			t.Fatalf("%s: Recover: %v", name, err)
		}
		if st.Allows("mallory", "root:shell") {
			t.Fatalf("%s: unproven permission granted", name)
		}
	}
}

// TestTornGarbageTailTruncated appends random garbage after the last
// complete record; recovery must truncate it and keep committed state.
func TestTornGarbageTailTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device.log")
	events := []Event{
		{EventProofReceived, "alice", "firmware:write", "proof-1"},
		{EventVerified, "alice", "firmware:write", "proof-1"},
		{EventCommit, "alice", "firmware:write", "proof-1"},
	}
	full := writeLog(t, path, events...)
	garbage := []byte{0x00, 0xFF, 0x13, 0x37, 0x42}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(garbage); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st, err := Recover(path)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !st.Allows("alice", "firmware:write") {
		t.Fatal("committed permission lost after garbage tail")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != int64(len(full)) {
		t.Fatalf("garbage tail not truncated: size = %d, want %d", fi.Size(), len(full))
	}
}

// TestRecoverMissingFile starts from no log at all.
func TestRecoverMissingFile(t *testing.T) {
	st, err := Recover(filepath.Join(t.TempDir(), "absent.log"))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if st.Allows("anyone", "anything") {
		t.Fatal("empty log produced a permission")
	}
}
