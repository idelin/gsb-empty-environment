package cachemodel

import (
	"bytes"
	"fmt"
)

// Model is the reference implementation: it tracks one dirty version per
// byte. Replaying the Page event log through a Model must reproduce the
// Page's exact dirty set and contents, and the disk image.
type Model struct {
	size      int
	version   uint64
	dirty     []uint64 // per-byte dirty version, 0 means clean
	data      []byte
	disk      []byte
	wbOpen    bool
	wbVersion uint64
}

func NewModel(size int) *Model {
	return &Model{
		size:  size,
		dirty: make([]uint64, size),
		data:  make([]byte, size),
		disk:  make([]byte, size),
	}
}

// Apply replays one cache event. It panics on any inconsistency, which makes
// it usable directly as a test oracle.
func (m *Model) Apply(e Event) {
	switch e.Op {
	case OpWrite:
		m.version++
		if e.Version != m.version {
			panic(fmt.Sprintf("model: write version got %d want %d", e.Version, m.version))
		}
		copy(m.data[e.Off:], e.Data)
		for i := e.Off; i < e.Off+len(e.Data); i++ {
			m.dirty[i] = m.version
		}
	case OpWritebackBegin:
		if m.wbOpen {
			panic("model: writeback already open")
		}
		m.wbOpen = true
		m.wbVersion = e.Version
	case OpWritebackComplete:
		if !m.wbOpen {
			panic("model: complete without begin")
		}
		for _, w := range e.Written {
			for i := w.Start; i < w.End; i++ {
				if m.dirty[i] != 0 && m.dirty[i] <= m.wbVersion {
					m.dirty[i] = 0
				}
			}
		}
		m.wbOpen = false
	case OpDiskWrite:
		copy(m.disk[e.Off:], e.Data)
	case OpEvict:
		for i, v := range m.dirty {
			if v != 0 {
				panic(fmt.Sprintf("model: evict returned with byte %d still dirty (version %d)", i, v))
			}
		}
		if m.wbOpen {
			panic("model: evict returned while writeback in flight")
		}
	}
}

// CheckPage compares the model against the real page: identical contents and
// an identical per-byte dirty map.
func (m *Model) CheckPage(p *Page) error {
	data := p.Data()
	if !bytes.Equal(data, m.data) {
		return fmt.Errorf("page data mismatch")
	}
	want := make([]bool, m.size)
	for i, v := range m.dirty {
		want[i] = v != 0
	}
	got := make([]bool, m.size)
	for _, iv := range p.DirtyIntervals() {
		for i := iv.Start; i < iv.End; i++ {
			got[i] = true
		}
	}
	for i := range want {
		if want[i] != got[i] {
			return fmt.Errorf("dirty state mismatch at byte %d: model=%v page=%v", i, want[i], got[i])
		}
	}
	return nil
}

// CheckDisk compares the model's disk image against the real disk.
func (m *Model) CheckDisk(d *Disk) error {
	if !bytes.Equal(d.Bytes(), m.disk) {
		return fmt.Errorf("disk image mismatch")
	}
	return nil
}
