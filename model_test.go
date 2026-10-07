package tail

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// ---------------------------------------------------------------------------
// Reference model
//
// The model replays the same operation script on an abstract filesystem
// (path -> identity -> content) and re-derives, from the spec, the exact
// sequence of lines the tailer must publish:
//   - a line is published only once its newline has been written;
//   - rotation/replacement drains the old handle before the new file starts
//     a new generation;
//   - truncation restarts the current file as a new generation;
//   - every complete line of every generation is published exactly once.
// ---------------------------------------------------------------------------

type fid int

type model struct {
	paths   map[string]fid
	content map[fid][]byte
	next    fid

	cur     fid
	hasCur  bool
	offset  int
	gen     int
	partial []byte

	expect []Line
}

func newModel() *model {
	return &model{paths: map[string]fid{}, content: map[fid][]byte{}}
}

func (m *model) create(path string) fid {
	m.next++
	m.paths[path] = m.next
	m.content[m.next] = nil
	return m.next
}

func (m *model) append(path, data string) {
	id, ok := m.paths[path]
	if !ok {
		id = m.create(path)
	}
	m.content[id] = append(m.content[id], data...)
}

func (m *model) truncate(path string) {
	if id, ok := m.paths[path]; ok {
		m.content[id] = m.content[id][:0]
	}
}

func (m *model) rename(src, dst string) {
	if id, ok := m.paths[src]; ok {
		delete(m.paths, src)
		m.paths[dst] = id
	}
}

// replace unlinks the old file (its content stays readable through an open
// handle) and puts a brand-new file at path.
func (m *model) replace(path, data string) {
	delete(m.paths, path)
	id := m.create(path)
	m.content[id] = []byte(data)
}

func (m *model) remove(path string) {
	delete(m.paths, path)
}

func (m *model) sync(path string) {
	id, ok := m.paths[path]
	if !ok {
		m.retire()
		return
	}
	if m.hasCur && id != m.cur {
		m.retire()
	}
	if !m.hasCur {
		m.hasCur = true
		m.cur = id
		m.gen++
		m.offset = 0
		m.partial = nil
	} else if len(m.content[m.cur]) < m.offset {
		m.gen++
		m.offset = 0
		m.partial = nil
	}
	m.read(m.cur)
}

func (m *model) retire() {
	if !m.hasCur {
		return
	}
	m.read(m.cur)
	m.hasCur = false
	m.partial = nil
}

func (m *model) read(id fid) {
	data := m.content[id]
	if m.offset > len(data) {
		m.offset = len(data)
	}
	p := data[m.offset:]
	m.offset = len(data)
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			m.partial = append(m.partial, p...)
			return
		}
		line := string(append(append([]byte{}, m.partial...), p[:i]...))
		m.partial = m.partial[:0]
		m.expect = append(m.expect, Line{Gen: m.gen, Text: line})
		p = p[i+1:]
	}
}

// ---------------------------------------------------------------------------
// Operation script
// ---------------------------------------------------------------------------

type opKind int

const (
	opAppend opKind = iota
	opTruncate
	opRotate // rename log -> log.N, no recreate
	opRotateCreate
	opReplace // atomic same-path replacement via temp + rename
	opRemove
	opSync
)

type op struct {
	kind opKind
	data string
}

func (o op) String() string {
	names := []string{"append", "truncate", "rotate", "rotate+create", "replace", "remove", "sync"}
	if o.data == "" {
		return names[o.kind]
	}
	return fmt.Sprintf("%s(%q)", names[o.kind], o.data)
}

// ---------------------------------------------------------------------------
// Harness: replays a script against a real directory and the model in
// lockstep, checking after every sync that the tailer has published exactly
// the model's lines and holds at most one handle.
// ---------------------------------------------------------------------------

type harness struct {
	t      *testing.T
	dir    string
	path   string
	model  *model
	tailer *Tailer
	got    []Line
	kept   int // hard-link keep-alive counter (pins inodes against reuse)
}

func newHarness(t *testing.T) *harness {
	dir, err := ioutil.TempDir("", "tailtest")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, dir: dir, path: filepath.Join(dir, "app.log"), model: newModel()}
	h.tailer = New(h.path, func(l Line) { h.got = append(h.got, l) })
	return h
}

func (h *harness) cleanup() {
	h.tailer.Close()
	os.RemoveAll(h.dir)
}

// keepAlive hard-links the current log file so its inode can never be
// recycled for a later file in the same test (which would forge an identity
// collision the tailer cannot be blamed for).
func (h *harness) keepAlive() {
	if _, err := os.Stat(h.path); err != nil {
		return
	}
	h.kept++
	os.Link(h.path, filepath.Join(h.dir, fmt.Sprintf(".keep-%d", h.kept)))
}

func (h *harness) apply(o op) {
	h.t.Helper()
	switch o.kind {
	case opAppend:
		f, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := f.WriteString(o.data); err != nil {
			h.t.Fatal(err)
		}
		f.Close()
		h.model.append(h.path, o.data)
	case opTruncate:
		h.keepAlive()
		if err := os.Truncate(h.path, 0); err != nil {
			h.t.Fatal(err)
		}
		h.model.truncate(h.path)
	case opRotate, opRotateCreate:
		h.keepAlive()
		dst := fmt.Sprintf("%s.%d", h.path, h.kept)
		if err := os.Rename(h.path, dst); err != nil {
			h.t.Fatal(err)
		}
		h.model.rename(h.path, dst)
		if o.kind == opRotateCreate {
			h.apply(op{kind: opAppend, data: o.data})
		}
	case opReplace:
		h.keepAlive()
		tmp := h.path + ".tmp"
		if err := ioutil.WriteFile(tmp, []byte(o.data), 0644); err != nil {
			h.t.Fatal(err)
		}
		if err := os.Rename(tmp, h.path); err != nil {
			h.t.Fatal(err)
		}
		h.model.replace(h.path, o.data)
	case opRemove:
		h.keepAlive()
		if err := os.Remove(h.path); err != nil {
			h.t.Fatal(err)
		}
		h.model.remove(h.path)
	case opSync:
		if err := h.tailer.Sync(); err != nil {
			h.t.Fatal(err)
		}
		h.model.sync(h.path)
		h.check()
	}
}

func (h *harness) check() {
	h.t.Helper()
	if !reflect.DeepEqual(h.got, h.model.expect) {
		h.t.Fatalf("lines diverge:\n got: %v\nwant: %v", h.got, h.model.expect)
	}
	if n := h.tailer.OpenHandles(); n > 1 {
		h.t.Fatalf("tailer holds %d handles, want <= 1", n)
	}
}

func (h *harness) run(script []op) {
	for i, o := range script {
		func() {
			defer func() {
				if r := recover(); r != nil {
					h.t.Fatalf("step %d %s: %v", i, o, r)
				}
			}()
			h.apply(o)
		}()
		if h.t.Failed() {
			h.t.Logf("failed at step %d of %d: %s", i, len(script), o)
			h.t.Logf("script: %v", script)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Deterministic edge-case script
// ---------------------------------------------------------------------------

func TestScripted(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()
	h.run([]op{
		{opAppend, "he"},          // partial line, nothing published
		{opSync, ""},              //
		{opAppend, "llo\nwo"},     // completes "hello", starts another
		{opSync, ""},              //
		{opAppend, "rld\n"},       // completes "world"
		{opSync, ""},              //
		{opTruncate, ""},          // same file, new generation
		{opAppend, "fresh\n"},     //
		{opSync, ""},              //
		{opRotate, ""},            // path vanishes; old handle drained
		{opSync, ""},              //
		{opAppend, "back\n"},      // file reappears: new generation
		{opSync, ""},              //
		{opRotateCreate, "n1\nn"}, // rotate + recreate with pending data
		{opSync, ""},              //
		{opAppend, "2\n"},         // completes "n2" in the new file
		{opSync, ""},              //
		{opReplace, "r1\nr2\n"},   // atomic same-path replacement
		{opSync, ""},              //
		{opRemove, ""},            //
		{opSync, ""},              //
	})
	want := []Line{
		{1, "hello"}, {1, "world"}, // gen 1: original file
		{2, "fresh"},         // gen 2: after truncation
		{3, "back"},          // gen 3: reappeared file
		{4, "n1"}, {4, "n2"}, // gen 4: rotate+create
		{5, "r1"}, {5, "r2"}, // gen 5: replacement
	}
	if !reflect.DeepEqual(h.got, want) {
		t.Fatalf("got %v, want %v", h.got, want)
	}
	if n := h.tailer.OpenHandles(); n != 0 {
		t.Fatalf("after final remove: %d handles open, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Randomised model-based test, replayable via TAIL_SEED
// ---------------------------------------------------------------------------

var fragments = []string{
	"a\n", "bb\n", "ccc\n", "\n", "x", "y", "z\n", "p", "q\nr\n", "st",
	"λ\n", "日本語\n", "long-line-without-newline", "tail\n",
}

func genScript(r *rand.Rand, n int) []op {
	exists := false
	script := make([]op, 0, n)
	for i := 0; i < n; i++ {
		choice := r.Intn(100)
		switch {
		case choice < 45:
			script = append(script, op{opAppend, fragments[r.Intn(len(fragments))]})
			exists = true
		case choice < 55 && exists:
			script = append(script, op{opTruncate, ""})
		case choice < 65 && exists:
			script = append(script, op{opRotate, ""})
			exists = false
		case choice < 75 && exists:
			script = append(script, op{opRotateCreate, fragments[r.Intn(len(fragments))]})
		case choice < 82 && exists:
			script = append(script, op{opReplace, fragments[r.Intn(len(fragments))]})
		case choice < 87 && exists:
			script = append(script, op{opRemove, ""})
			exists = false
		default:
			script = append(script, op{opSync, ""})
		}
	}
	script = append(script, op{opSync, ""})
	return script
}

func TestModelRandom(t *testing.T) {
	seed := int64(1)
	if s := os.Getenv("TAIL_SEED"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("bad TAIL_SEED: %v", err)
		}
		seed = v
	}
	const scripts = 200
	for i := 0; i < scripts; i++ {
		s := seed + int64(i)
		r := rand.New(rand.NewSource(s))
		script := genScript(r, 60)
		func() {
			h := newHarness(t)
			defer h.cleanup()
			h.run(script)
		}()
		if t.Failed() {
			t.Fatalf("seed %d failed; replay with TAIL_SEED=%d", s, s)
		}
		if i == 0 {
			t.Logf("first script (TAIL_SEED=%d): %v", s, script)
		}
	}
}

// ---------------------------------------------------------------------------
// Handle leak check: fd count of the process must be stable across
// rotations, replacements and removals.
// ---------------------------------------------------------------------------

func fdCount(t *testing.T) int {
	f, err := os.Open("/dev/fd")
	if err != nil {
		t.Skip("cannot open /dev/fd:", err)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		t.Skip("cannot list /dev/fd:", err)
	}
	return len(names)
}

func TestNoHandleLeak(t *testing.T) {
	before := fdCount(t)
	h := newHarness(t)
	for i := 0; i < 50; i++ {
		h.run([]op{
			{opAppend, "round\n"},
			{opSync, ""},
			{opRotateCreate, "next\n"},
			{opSync, ""},
			{opReplace, "again\n"},
			{opSync, ""},
			{opRemove, ""},
			{opSync, ""},
		})
	}
	if err := h.tailer.Close(); err != nil {
		t.Fatal(err)
	}
	if n := h.tailer.OpenHandles(); n != 0 {
		t.Fatalf("%d handles still open after Close", n)
	}
	os.RemoveAll(h.dir)
	if after := fdCount(t); after != before {
		t.Fatalf("fd count changed: before=%d after=%d", before, after)
	}
}
