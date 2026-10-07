package sortexec

import "sync"

// Tracker counts file and iterator handles so tests can verify that every
// acquired resource is released exactly once (open counts return to zero
// and never go negative).
type Tracker struct {
	mu         sync.Mutex
	writeOpen  int
	readOpen   int
	iterOpen   int
	writeTotal int
	readTotal  int
	iterTotal  int
}

func (t *Tracker) writeOpened() { t.add(1, 0, 0) }
func (t *Tracker) writeClosed() { t.add(-1, 0, 0) }
func (t *Tracker) readOpened()  { t.add(0, 1, 0) }
func (t *Tracker) readClosed()  { t.add(0, -1, 0) }
func (t *Tracker) iterOpened()  { t.add(0, 0, 1) }
func (t *Tracker) iterClosed()  { t.add(0, 0, -1) }

func (t *Tracker) add(w, r, it int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeOpen += w
	t.readOpen += r
	t.iterOpen += it
	if w > 0 {
		t.writeTotal++
	}
	if r > 0 {
		t.readTotal++
	}
	if it > 0 {
		t.iterTotal++
	}
}

// OpenFiles reports currently open spill files (readers and writers).
func (t *Tracker) OpenFiles() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeOpen + t.readOpen
}

// OpenIterators reports currently open merge iterators.
func (t *Tracker) OpenIterators() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.iterOpen
}

// IteratorsOpened reports how many iterators were ever created.
func (t *Tracker) IteratorsOpened() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.iterTotal
}

// Balanced reports whether every acquired handle has been released.
func (t *Tracker) Balanced() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeOpen == 0 && t.readOpen == 0 && t.iterOpen == 0
}

// countedWriteFile closes the underlying file at most once and keeps the
// tracker balanced even if Close is called repeatedly.
type countedWriteFile struct {
	w    WriteFile
	tr   *Tracker
	once sync.Once
	err  error
}

func newCountedWriteFile(w WriteFile, tr *Tracker) *countedWriteFile {
	tr.writeOpened()
	return &countedWriteFile{w: w, tr: tr}
}

func (c *countedWriteFile) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *countedWriteFile) Close() error {
	c.once.Do(func() {
		c.err = c.w.Close()
		c.tr.writeClosed()
	})
	return c.err
}

// countedReadFile mirrors countedWriteFile for readers.
type countedReadFile struct {
	r    ReadFile
	tr   *Tracker
	once sync.Once
	err  error
}

func newCountedReadFile(r ReadFile, tr *Tracker) *countedReadFile {
	tr.readOpened()
	return &countedReadFile{r: r, tr: tr}
}

func (c *countedReadFile) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *countedReadFile) Close() error {
	c.once.Do(func() {
		c.err = c.r.Close()
		c.tr.readClosed()
	})
	return c.err
}
