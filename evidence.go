// Package material 实现证据材料库：每项证据带内容摘要、版本与签收人集合。
// 内容改动会产生新版本并清空新版本的签收，旧版本及其签收记录完整保留。
package material

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// 错误定义。
var (
	ErrEvidenceNotFound = errors.New("material: evidence not found")
	ErrEvidenceExists   = errors.New("material: evidence already exists")
	ErrVersionNotFound  = errors.New("material: version not found")
	ErrSignerNotFound   = errors.New("material: signer not found on version")
	ErrEmptySummary     = errors.New("material: content summary must not be empty")
	ErrEmptySigner      = errors.New("material: signer must not be empty")
)

// Version 是证据的一次内容快照及其签收记录。
type Version struct {
	Number    int
	Summary   string
	Signers   map[string]bool
	Published bool
	CreatedAt time.Time
}

// SignerList 返回排序后的签收人列表，便于断言与展示。
func (v *Version) SignerList() []string {
	out := make([]string, 0, len(v.Signers))
	for s := range v.Signers {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Evidence 是一项证据的全部版本历史。
type Evidence struct {
	ID       string
	versions map[int]*Version
	current  int
}

// Current 返回当前版本号。
func (e *Evidence) Current() int { return e.current }

// Version 返回指定版本；不存在时返回 nil。
func (e *Evidence) Version(n int) *Version { return e.versions[n] }

// Versions 返回按版本号升序排列的全部历史版本。
func (e *Evidence) Versions() []*Version {
	out := make([]*Version, 0, len(e.versions))
	for _, v := range e.versions {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// Library 是证据材料库，负责版本演进与签收登记。
type Library struct {
	mu        sync.RWMutex
	evidences map[string]*Evidence
	graph     *Graph
}

// NewLibrary 创建空材料库，并附带一张版本图用于核对。
func NewLibrary() *Library {
	return &Library{
		evidences: make(map[string]*Evidence),
		graph:     NewGraph(),
	}
}

// Graph 返回材料库的版本图。
func (l *Library) Graph() *Graph { return l.graph }

// Create 登记新证据，初始版本为 1，签收人集合为空。
func (l *Library) Create(id, summary string) error {
	if summary == "" {
		return ErrEmptySummary
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.evidences[id]; ok {
		return ErrEvidenceExists
	}
	e := &Evidence{ID: id, versions: make(map[int]*Version), current: 1}
	e.versions[1] = &Version{Number: 1, Summary: summary, Signers: map[string]bool{}, CreatedAt: time.Now()}
	l.evidences[id] = e
	l.graph.AddVersion(id, 1)
	return nil
}

// Update 改动内容摘要：产生新版本，新版本签收人集合为空；
// 旧版本及其签收记录原样保留。
func (l *Library) Update(id, summary string) (int, error) {
	if summary == "" {
		return 0, ErrEmptySummary
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.evidences[id]
	if !ok {
		return 0, ErrEvidenceNotFound
	}
	next := e.current + 1
	e.versions[next] = &Version{Number: next, Summary: summary, Signers: map[string]bool{}, CreatedAt: time.Now()}
	e.current = next
	l.graph.AddVersion(id, next)
	return next, nil
}

// Sign 在指定版本上登记签收人。
func (l *Library) Sign(id string, version int, signer string) error {
	if signer == "" {
		return ErrEmptySigner
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	v, err := l.versionLocked(id, version)
	if err != nil {
		return err
	}
	v.Signers[signer] = true
	l.graph.RecordSign(id, version, signer)
	return nil
}

// Withdraw 撤回指定版本上某签收人的签收。
func (l *Library) Withdraw(id string, version int, signer string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, err := l.versionLocked(id, version)
	if err != nil {
		return err
	}
	if !v.Signers[signer] {
		return ErrSignerNotFound
	}
	delete(v.Signers, signer)
	l.graph.RecordWithdraw(id, version, signer)
	return nil
}

// Snapshot 返回证据的只读视图；不存在时 ok=false。
func (l *Library) Snapshot(id string) (EvidenceView, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.evidences[id]
	if !ok {
		return EvidenceView{}, false
	}
	view := EvidenceView{ID: e.ID, Current: e.current, Versions: make(map[int]VersionView, len(e.versions))}
	for n, v := range e.versions {
		view.Versions[n] = VersionView{
			Number:  v.Number,
			Summary: v.Summary,
			Signers: v.SignerList(),
		}
	}
	return view, true
}

// EvidenceView 是证据的只读快照。
type EvidenceView struct {
	ID       string
	Current  int
	Versions map[int]VersionView
}

// VersionView 是单个版本的只读快照。
type VersionView struct {
	Number  int
	Summary string
	Signers []string
}

func (l *Library) versionLocked(id string, version int) (*Version, error) {
	e, ok := l.evidences[id]
	if !ok {
		return nil, ErrEvidenceNotFound
	}
	v, ok := e.versions[version]
	if !ok {
		return nil, fmt.Errorf("material: version not found: %s@%d", id, version)
	}
	return v, nil
}
