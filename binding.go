package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrCrossPatient       = errors.New("netsec: barcode already bound to a different patient")
	ErrTimeConflict       = errors.New("netsec: barcode collection time conflicts with existing binding")
	ErrBarcodeReuse       = errors.New("netsec: barcode reuse with different test items")
	ErrBindingNotFound    = errors.New("netsec: no binding for barcode")
	ErrBindingMismatch    = errors.New("netsec: report fields do not match the sample binding")
	ErrInstrumentMismatch = errors.New("netsec: instrument raw record missing or does not match binding")
)

// Binding 将样本条码与患者、采集时间、检验项目不可变地绑定。
type Binding struct {
	Barcode     string
	PatientID   string
	CollectedAt time.Time
	Items       []string
	BoundAt     time.Time
}

// ID 返回绑定内容的确定性摘要，报告通过它锚定唯一样本。
func (b Binding) ID() string {
	items := append([]string(nil), b.Items...)
	sort.Strings(items)
	h := sha256.Sum256([]byte(strings.Join([]string{
		b.Barcode,
		b.PatientID,
		b.CollectedAt.UTC().Format(time.RFC3339Nano),
		strings.Join(items, ","),
	}, "|")))
	return hex.EncodeToString(h[:])
}

func (b Binding) sameSample(other Binding) bool {
	return b.PatientID == other.PatientID &&
		b.CollectedAt.Equal(other.CollectedAt) &&
		equalItems(b.Items, other.Items)
}

func equalItems(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// InstrumentRecord 是仪器侧留存的原始记录，发布前用于独立核对。
type InstrumentRecord struct {
	Barcode     string
	PatientID   string
	CollectedAt time.Time
	Items       []string
	RawRef      string
}

// InstrumentStore 由检验服务接入仪器原始记录库实现。
type InstrumentStore interface {
	RawRecord(barcode string) (InstrumentRecord, bool)
}

// MemoryInstrumentStore 是便于测试与演示的内存实现。
type MemoryInstrumentStore struct {
	mu      sync.RWMutex
	records map[string]InstrumentRecord
}

func NewMemoryInstrumentStore() *MemoryInstrumentStore {
	return &MemoryInstrumentStore{records: make(map[string]InstrumentRecord)}
}

func (s *MemoryInstrumentStore) Put(rec InstrumentRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.Barcode] = rec
}

func (s *MemoryInstrumentStore) RawRecord(barcode string) (InstrumentRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[barcode]
	return rec, ok
}

// Binder 维护条码绑定历史，并在报告发布前执行一致性核对。
type Binder struct {
	mu         sync.RWMutex
	byBarcode  map[string][]Binding
	instrument InstrumentStore
	now        func() time.Time
}

func NewBinder(instrument InstrumentStore) *Binder {
	return &Binder{
		byBarcode:  make(map[string][]Binding),
		instrument: instrument,
		now:        time.Now,
	}
}

// Bind 登记样本绑定。完全相同的重复登记是幂等的；
// 跨患者、采集时间冲突或项目不一致的复用均被拒绝。
func (r *Binder) Bind(barcode, patientID string, collectedAt time.Time, items []string) (Binding, error) {
	if barcode == "" || patientID == "" {
		return Binding{}, fmt.Errorf("netsec: barcode and patient are required")
	}
	if len(items) == 0 {
		return Binding{}, fmt.Errorf("netsec: at least one test item is required")
	}
	nb := Binding{
		Barcode:     barcode,
		PatientID:   patientID,
		CollectedAt: collectedAt.UTC(),
		Items:       append([]string(nil), items...),
		BoundAt:     r.now().UTC(),
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.byBarcode[barcode] {
		switch {
		case existing.PatientID != patientID:
			return Binding{}, fmt.Errorf("%w: barcode %s bound to patient %s", ErrCrossPatient, barcode, existing.PatientID)
		case !existing.CollectedAt.Equal(nb.CollectedAt):
			return Binding{}, fmt.Errorf("%w: barcode %s already collected at %s", ErrTimeConflict, barcode, existing.CollectedAt.Format(time.RFC3339))
		case !equalItems(existing.Items, nb.Items):
			return Binding{}, fmt.Errorf("%w: barcode %s", ErrBarcodeReuse, barcode)
		default:
			return existing, nil
		}
	}
	r.byBarcode[barcode] = append(r.byBarcode[barcode], nb)
	return nb, nil
}

// Lookup 返回条码的当前绑定。
func (r *Binder) Lookup(barcode string) (Binding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := r.byBarcode[barcode]
	if len(list) == 0 {
		return Binding{}, false
	}
	return list[len(list)-1], true
}

// History 返回条码的全部绑定历史，供审计与发布前核对。
func (r *Binder) History(barcode string) []Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Binding(nil), r.byBarcode[barcode]...)
}

// checkPublish 在发布前核对绑定与仪器原始记录。
// 报告声称的患者、采集时间、项目必须与绑定及仪器记录一致。
func (r *Binder) checkPublish(barcode, patientID string, collectedAt time.Time, items []string) (Binding, error) {
	b, ok := r.Lookup(barcode)
	if !ok {
		return Binding{}, fmt.Errorf("%w: %s", ErrBindingNotFound, barcode)
	}
	claimed := Binding{PatientID: patientID, CollectedAt: collectedAt.UTC(), Items: items}
	if !b.sameSample(claimed) {
		return Binding{}, fmt.Errorf("%w: barcode %s bound to patient %s", ErrBindingMismatch, barcode, b.PatientID)
	}
	if r.instrument != nil {
		rec, ok := r.instrument.RawRecord(barcode)
		if !ok {
			return Binding{}, fmt.Errorf("%w: no raw record for %s", ErrInstrumentMismatch, barcode)
		}
		raw := Binding{PatientID: rec.PatientID, CollectedAt: rec.CollectedAt.UTC(), Items: rec.Items}
		if !b.sameSample(raw) {
			return Binding{}, fmt.Errorf("%w: barcode %s raw record belongs to patient %s", ErrInstrumentMismatch, barcode, rec.PatientID)
		}
	}
	return b, nil
}
