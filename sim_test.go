package rootstore

import "testing"

func TestSimLayers(t *testing.T) {
	// Honest drive: Sync hardens immediately.
	fs := NewSimFS()
	f, _ := fs.Open("f")
	if _, err := f.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	if w, s, r := fs.LayerSizes("f"); w != 5 || s != 0 || r != 0 {
		t.Fatalf("after write: layers=(%d,%d,%d), want (5,0,0)", w, s, r)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if w, s, r := fs.LayerSizes("f"); w != 5 || s != 5 || r != 5 {
		t.Fatalf("after sync: layers=(%d,%d,%d), want (5,5,5)", w, s, r)
	}
	fs.Crash(PowerLoss)
	buf := make([]byte, 5)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "hello" {
		t.Fatalf("after power loss: read=%q err=%v", buf, err)
	}

	// Volatile disk cache: Sync stages, only Settle hardens.
	fs2 := NewSimFS()
	fs2.VolatileDiskCache = true
	g, _ := fs2.Open("g")
	g.WriteAt([]byte("hello"), 0)
	g.Sync()
	if w, s, r := fs2.LayerSizes("g"); w != 5 || s != 5 || r != 0 {
		t.Fatalf("volatile cache after sync: layers=(%d,%d,%d), want (5,5,0)", w, s, r)
	}
	fs2.Crash(PowerLoss)
	if w, _, _ := fs2.LayerSizes("g"); w != 0 {
		t.Fatalf("volatile cache lost data must vanish on power loss, written=%d", w)
	}

	// Same, but Settle before the power loss.
	fs3 := NewSimFS()
	fs3.VolatileDiskCache = true
	h, _ := fs3.Open("h")
	h.WriteAt([]byte("hello"), 0)
	h.Sync()
	fs3.Settle()
	fs3.Crash(PowerLoss)
	if w, _, _ := fs3.LayerSizes("h"); w != 5 {
		t.Fatalf("settled data must survive power loss, written=%d", w)
	}

	// Process crash: unsynced writes survive (OS page cache persists).
	fs4 := NewSimFS()
	p, _ := fs4.Open("p")
	p.WriteAt([]byte("world"), 0)
	fs4.Crash(ProcessCrash)
	buf = make([]byte, 5)
	if _, err := p.ReadAt(buf, 0); err != nil || string(buf) != "world" {
		t.Fatalf("process crash must keep written bytes: read=%q err=%v", buf, err)
	}

	// Lying drive: dropped Sync means nothing is recoverable.
	fs5 := NewSimFS()
	fs5.Hook = func(op *Op) {
		if op.Kind == OpSync {
			op.Drop = true
		}
	}
	q, _ := fs5.Open("q")
	q.WriteAt([]byte("hello"), 0)
	q.Sync()
	if _, s, r := fs5.LayerSizes("q"); s != 0 || r != 0 {
		t.Fatalf("dropped sync: layers synced=%d recoverable=%d, want 0,0", s, r)
	}
	fs5.Crash(PowerLoss)
	if w, _, _ := fs5.LayerSizes("q"); w != 0 {
		t.Fatalf("dropped sync + power loss must lose data, written=%d", w)
	}
}
