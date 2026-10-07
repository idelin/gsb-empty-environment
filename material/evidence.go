// Package material 实现证据材料库：每项证据带内容摘要、版本与签收人集合，
// 内容改动产生新版本并清空新版签收，旧版本记录完整保留。
package material

import (
	"errors"
	"fmt"
	"sync"
)

// Digest 是证据内容的摘要（如哈希值）。
type Digest string

// Ref 指向某项证据的特定版本，是版本图与批量发布中的最小单元。
type Ref struct {
	EvidenceID string
	Version    int
}

func (r Ref) String() string {
	return fmt.Sprintf("%s@v%d", r.EvidenceID, r.Version)
}

// VersionRecord 记录证据某一版本的不可变快照。
type VersionRecord struct {
	Version   int
	Digest    Digest
	Signers   map[string]struct{}
	Deps      []Ref
	Withdrawn bool
	Published bool
}

// Signed 报告该版本是否已被签收。
func (v *VersionRecord) Signed() bool { return len(v.Signers) > 0 }

// Evidence 是一项证据的全部版本历史。
type Evidence struct {
	ID       string
	Current  int
	Versions map[int]*VersionRecord
}

// Record 返回指定版本的记录，不存在时返回 nil。
func (e *Evidence) Record(version int) *VersionRecord {
	return e.Versions[version]
}

// Library 是线程安全的证据材料库。
type Library struct {
	mu    sync.RWMutex
	items map[string]*Evidence
}

// NewLibrary 创建空材料库。
func NewLibrary() *Library {
	return &Library{items: make(map[string]*Evidence)}
}

var (
	ErrEvidenceExists   = errors.New("material: evidence already exists")
	ErrEvidenceNotFound = errors.New("material: evidence not found")
	ErrVersionNotFound  = errors.New("material: version not found")
	ErrVersionWithdrawn = errors.New("material: version withdrawn")
)

// AddEvidence 登记新证据，初始版本为 v1。
func (l *Library) AddEvidence(id string, digest Digest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.items[id]; ok {
		return fmt.Errorf("%v: %s", ErrEvidenceExists, id)
	}
	l.items[id] = &Evidence{
		ID:      id,
		Current: 1,
		Versions: map[int]*VersionRecord{
			1: {Version: 1, Digest: digest, Signers: map[string]struct{}{}},
		},
	}
	return nil
}

// UpdateContent 修改证据内容：产生新版本，新版本的签收集合为空，
// 旧版本记录（含签收人）原样保留。
func (l *Library) UpdateContent(id string, digest Digest) (Ref, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.items[id]
	if !ok {
		return Ref{}, fmt.Errorf("%v: %s", ErrEvidenceNotFound, id)
	}
	next := e.Current + 1
	e.Versions[next] = &VersionRecord{
		Version: next,
		Digest:  digest,
		Signers: map[string]struct{}{},
	}
	e.Current = next
	return Ref{EvidenceID: id, Version: next}, nil
}

// Sign 为指定版本添加签收人。已撤回版本拒绝签收。
func (l *Library) Sign(id string, version int, signer string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.recordLocked(id, version)
	if err != nil {
		return err
	}
	if rec.Withdrawn {
		return fmt.Errorf("%v: %s@v%d", ErrVersionWithdrawn, id, version)
	}
	rec.Signers[signer] = struct{}{}
	return nil
}

// Withdraw 撤回指定版本；撤回是终态，记录保留但不可签收、不可发布。
func (l *Library) Withdraw(id string, version int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.recordLocked(id, version)
	if err != nil {
		return err
	}
	rec.Withdrawn = true
	return nil
}

// SetDependencies 冻结指定版本的依赖集合（依赖按精确版本钉住）。
func (l *Library) SetDependencies(id string, version int, deps []Ref) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.recordLocked(id, version)
	if err != nil {
		return err
	}
	frozen := make([]Ref, len(deps))
	copy(frozen, deps)
	rec.Deps = frozen
	return nil
}

// Record 返回指定版本记录的只读快照。
func (l *Library) Record(id string, version int) (VersionRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	rec, err := l.recordLocked(id, version)
	if err != nil {
		return VersionRecord{}, err
	}
	return cloneRecord(rec), nil
}

// Current 返回证据当前版本号。
func (l *Library) Current(id string) (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.items[id]
	if !ok {
		return 0, fmt.Errorf("%v: %s", ErrEvidenceNotFound, id)
	}
	return e.Current, nil
}

func (l *Library) recordLocked(id string, version int) (*VersionRecord, error) {
	e, ok := l.items[id]
	if !ok {
		return nil, fmt.Errorf("%v: %s", ErrEvidenceNotFound, id)
	}
	rec, ok := e.Versions[version]
	if !ok {
		return nil, fmt.Errorf("%v: %s@v%d", ErrVersionNotFound, id, version)
	}
	return rec, nil
}

func cloneRecord(rec *VersionRecord) VersionRecord {
	out := *rec
	out.Signers = make(map[string]struct{}, len(rec.Signers))
	for s := range rec.Signers {
		out.Signers[s] = struct{}{}
	}
	out.Deps = append([]Ref(nil), rec.Deps...)
	return out
}
