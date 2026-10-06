package logtail

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// model is the specification side of the check: it derives the expected
// line stream directly from filesystem snapshots, using the same
// identity/offset rules the tailer is required to implement:
//
//   - identity change at the watched path -> drain old generation, begin new one;
//   - size below the consumed offset -> truncation -> new generation;
//   - only newline-terminated bytes are published; a generation's
//     unterminated tail is dropped when the generation ends.
type model struct {
	fs   *FS
	path string

	hasCur  bool
	ino     Ino
	off     int64 // raw bytes consumed in the current generation
	pending []byte
	gen     uint64
	lines   []Line
}

func newModel(fs *FS, path string) *model { return &model{fs: fs, path: path} }

func (m *model) poll() {
	ino, size, ok := m.fs.Stat(m.path)
	switch {
	case !ok:
		m.drain()
	case !m.hasCur:
		m.begin(ino)
	case m.ino != ino:
		m.drain()
		m.begin(ino)
	case size < m.off:
		m.begin(ino)
	}
	m.read()
}

func (m *model) begin(ino Ino) {
	m.hasCur = true
	m.ino = ino
	m.off = 0
	m.pending = m.pending[:0]
	m.gen++
}

func (m *model) drain() {
	if !m.hasCur {
		return
	}
	m.read()
	m.hasCur = false
	m.pending = m.pending[:0]
}

func (m *model) read() {
	if !m.hasCur {
		return
	}
	data, ok := m.fs.Snapshot(m.ino)
	if !ok || m.off >= int64(len(data)) {
		return
	}
	m.pending = append(m.pending, data[m.off:]...)
	m.off = int64(len(data))
	for {
		i := bytes.IndexByte(m.pending, '\n')
		if i < 0 {
			break
		}
		m.lines = append(m.lines, Line{Gen: m.gen, Ino: m.ino, Text: string(m.pending[:i])})
		m.pending = m.pending[i+1:]
	}
}

// dumpOps renders an op log as Go source so a failing random session
// can be pasted into a test and replayed verbatim.
func dumpOps(ops []Op) string {
	var b strings.Builder
	for _, op := range ops {
		fmt.Fprintf(&b, "\t{Kind: Op%s, Path: %q, To: %q, Data: %q},\n",
			strings.Title(op.Kind.String()), op.Path, op.To, op.Data)
	}
	return b.String()
}

var modelPaths = []string{"app.log", "app.log.1", "app.log.2"}

func randChunk(rng *rand.Rand) []byte {
	var b strings.Builder
	for n := rng.Intn(5); n >= 0; n-- {
		switch rng.Intn(4) {
		case 0:
			b.WriteByte('\n')
		default:
			fmt.Fprintf(&b, "w%d", rng.Intn(10))
		}
	}
	return []byte(b.String())
}

func genOp(rng *rand.Rand, fs *FS) Op {
	var existing, missing []string
	for _, p := range modelPaths {
		if _, _, ok := fs.Stat(p); ok {
			existing = append(existing, p)
		} else {
			missing = append(missing, p)
		}
	}
	var kinds []OpKind
	if len(missing) > 0 {
		kinds = append(kinds, OpCreate)
	}
	if len(existing) > 0 {
		kinds = append(kinds, OpAppend, OpAppend, OpAppend, OpTruncate, OpRename, OpRemove)
	}
	pick := func(ps []string) string { return ps[rng.Intn(len(ps))] }
	switch kinds[rng.Intn(len(kinds))] {
	case OpCreate:
		return Op{Kind: OpCreate, Path: pick(missing), Data: randChunk(rng)}
	case OpAppend:
		return Op{Kind: OpAppend, Path: pick(existing), Data: randChunk(rng)}
	case OpTruncate:
		return Op{Kind: OpTruncate, Path: pick(existing)}
	case OpRemove:
		return Op{Kind: OpRemove, Path: pick(existing)}
	default: // OpRename
		return Op{Kind: OpRename, Path: pick(existing), To: modelPaths[rng.Intn(len(modelPaths))]}
	}
}

// TestModelAgainstRandomOps replays randomized file-operation sessions
// and checks, per session:
//   - the tailer's delivered stream equals the model's expected stream
//     (every complete line of every generation delivered exactly once,
//     in order, with correct generation/identity attribution);
//   - every handle opened along the way is closed by the end.
func TestModelAgainstRandomOps(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		fs := NewFS()
		tl := NewTailer(fs, "app.log")
		m := newModel(fs, "app.log")
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 150; i++ {
			if err := fs.Apply(genOp(rng, fs)); err != nil {
				t.Fatalf("seed %d op %d: %v\nops:\n%s", seed, i, err, dumpOps(fs.Ops()))
			}
			tl.Poll()
			m.poll()
			if rng.Intn(5) == 0 { // polling when quiescent must be a no-op
				tl.Poll()
				m.poll()
			}
		}
		tl.Close()
		if n := fs.OpenHandles(); n != 0 {
			t.Fatalf("seed %d: %d handles leaked\nops:\n%s", seed, n, dumpOps(fs.Ops()))
		}
		if !reflect.DeepEqual(tl.Lines, m.lines) {
			t.Fatalf("seed %d: delivered stream diverges from model\ntailer: %+v\nmodel:  %+v\nops:\n%s",
				seed, tl.Lines, m.lines, dumpOps(fs.Ops()))
		}
		// The recorded session must replay to the identical stream.
		replayed, open := runOps(fs.Ops())
		if open != 0 {
			t.Fatalf("seed %d: replay leaked %d handles", seed, open)
		}
		if !reflect.DeepEqual(tl.Lines, replayed) {
			t.Fatalf("seed %d: replay diverged\nlive:     %+v\nreplayed: %+v", seed, tl.Lines, replayed)
		}
	}
}
