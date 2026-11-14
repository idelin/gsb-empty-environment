package netsec

import (
	"fmt"
	"strings"
	"sync"
)

// HistoryEntry 记录一条已授权并应用的操作。
type HistoryEntry struct {
	Version   uint64
	Op        Operation
	SubjectID string
}

// document 是服务端的文档状态：内容、历史与逐版本快照。
type document struct {
	content  []rune
	history  []HistoryEntry
	snapshot map[uint64][]rune // version -> 内容快照，用于回滚
}

// Server 是演示用协同编辑服务端，展示 Authorizer 的调用方式：
// 所有操作先经授权过滤，再做操作变换（OT）合并。
type Server struct {
	auth *Authorizer
	mu   sync.Mutex
	docs map[string]*document
}

func NewServer(auth *Authorizer) *Server {
	return &Server{auth: auth, docs: make(map[string]*document)}
}

// CreateDocument 注册文档并设置初始内容。
func (s *Server) CreateDocument(docID, owner, initial string) error {
	if err := s.auth.RegisterDocument(docID, owner); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[docID] = &document{
		content:  []rune(initial),
		snapshot: map[uint64][]rune{0: []rune(initial)},
	}
	return nil
}

// Submit 是操作入口：先授权（绑定文档/会话/主体/版本、权限过滤、
// 防重放），授权通过后才对并发操作做位置变换并应用。
func (s *Server) Submit(op Operation) (uint64, error) {
	version, err := s.auth.Authorize(op)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[op.DocID]

	// 操作变换：把基于旧版本的操作位置平移到当前内容坐标。
	transformed := op
	for _, entry := range doc.history {
		if entry.Version <= op.BaseVersion {
			continue
		}
		transformed.Position = transformPosition(transformed.Position, entry.Op)
	}
	if err := doc.apply(transformed); err != nil {
		return 0, err
	}
	doc.history = append(doc.history, HistoryEntry{
		Version:   version,
		Op:        transformed,
		SubjectID: op.SubjectID,
	})
	doc.snapshot[version] = append([]rune(nil), doc.content...)
	return version, nil
}

// Content 返回文档当前内容。
func (s *Server) Content(docID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.docs[docID].content)
}

// History 返回文档历史，需通过历史读取授权。
func (s *Server) History(docID, sessionID string) ([]HistoryEntry, error) {
	if err := s.auth.AuthorizeHistory(docID, sessionID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HistoryEntry(nil), s.docs[docID].history...), nil
}

// Rollback 将文档回滚到目标版本，需通过回滚授权（owner）。
func (s *Server) Rollback(docID, sessionID string, targetVersion uint64) error {
	if err := s.auth.AuthorizeRollback(docID, sessionID, targetVersion); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[docID]
	snap, ok := doc.snapshot[targetVersion]
	if !ok {
		return fmt.Errorf("netsec: no snapshot at version %d", targetVersion)
	}
	doc.content = append([]rune(nil), snap...)
	kept := doc.history[:0]
	for _, entry := range doc.history {
		if entry.Version <= targetVersion {
			kept = append(kept, entry)
		}
	}
	doc.history = kept
	for version := range doc.snapshot {
		if version > targetVersion {
			delete(doc.snapshot, version)
		}
	}
	s.auth.CommitRollback(docID, targetVersion)
	return nil
}

// transformPosition 按一条已应用操作平移位置坐标（简化 OT）。
func transformPosition(pos int, applied Operation) int {
	switch applied.Type {
	case OpInsert:
		if applied.Position <= pos {
			return pos + len([]rune(applied.Text))
		}
	case OpDelete:
		if applied.Position < pos {
			shift := applied.Length
			if pos-applied.Position < shift {
				shift = pos - applied.Position
			}
			return pos - shift
		}
	}
	return pos
}

func (d *document) apply(op Operation) error {
	switch op.Type {
	case OpInsert:
		if op.Position > len(d.content) {
			return fmt.Errorf("netsec: insert position %d out of range %d",
				op.Position, len(d.content))
		}
		text := []rune(op.Text)
		next := make([]rune, 0, len(d.content)+len(text))
		next = append(next, d.content[:op.Position]...)
		next = append(next, text...)
		next = append(next, d.content[op.Position:]...)
		d.content = next
	case OpDelete:
		if op.Position+op.Length > len(d.content) {
			return fmt.Errorf("netsec: delete range [%d,%d) out of range %d",
				op.Position, op.Position+op.Length, len(d.content))
		}
		d.content = append(d.content[:op.Position], d.content[op.Position+op.Length:]...)
	}
	return nil
}

// Contains 报告文档内容是否包含子串，便于断言敏感段落是否被合回。
func (s *Server) Contains(docID, substr string) bool {
	return strings.Contains(s.Content(docID), substr)
}
