package freeze

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

// store 模拟发布宿主的名称解析：内容可在事务中途被替换。
type store struct {
	contents map[string][]byte
	types    map[string]string
	calls    map[string]int
}

func newStore() *store {
	return &store{
		contents: map[string][]byte{},
		types:    map[string]string{},
		calls:    map[string]int{},
	}
}

func (s *store) put(name, typ string, content []byte) {
	s.contents[name] = content
	s.types[name] = typ
}

func (s *store) resolve(name string) ([]byte, string, error) {
	s.calls[name]++
	content, ok := s.contents[name]
	if !ok {
		return nil, "", fmt.Errorf("unknown name %q", name)
	}
	return content, s.types[name], nil
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func entryByName(m *Manifest, name string) (Entry, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// 正常路径：菱形共享依赖 + 引用环，清单包含全部可达节点且摘要已固定。
func TestFreezeClosureWithSharedDepAndCycle(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))
	s.put("dict-a", "dict", []byte("dict-a-v1"))
	s.put("dict-b", "dict", []byte("dict-b-v1"))
	s.put("model", "model", []byte("model-v1"))

	graph := map[string]Node{
		"rule":   {Type: "rule", Deps: []string{"dict-a", "dict-b"}},
		"dict-a": {Type: "dict", Deps: []string{"model"}},
		"dict-b": {Type: "dict", Deps: []string{"model"}},
		"model":  {Type: "model", Deps: []string{"rule"}}, // 环回根节点
	}

	m, err := Freeze("rule", graph, s.resolve)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if len(m.Entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(m.Entries))
	}
	for _, name := range []string{"rule", "dict-a", "dict-b", "model"} {
		e, ok := entryByName(m, name)
		if !ok {
			t.Fatalf("missing entry %s", name)
		}
		if e.Digest != sha256hex(s.contents[name]) {
			t.Errorf("%s digest = %s, want pinned content digest", name, e.Digest)
		}
	}
	if m.Digest == "" {
		t.Error("manifest digest is empty")
	}
}

// 解析过程中换目标：共享依赖第二次解析时内容被替换，必须拒绝发布。
func TestDigestSwapMidResolutionRejected(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))
	s.put("dict-a", "dict", []byte("dict-a-v1"))
	s.put("dict-b", "dict", []byte("dict-b-v1"))
	s.put("model", "model", []byte("model-v1"))

	graph := map[string]Node{
		"rule":   {Deps: []string{"dict-a", "dict-b"}},
		"dict-a": {Deps: []string{"model"}},
		"dict-b": {Deps: []string{"model"}},
		"model":  {},
	}

	// model 第一次被固定后，宿主侧将其改指向新内容。
	swapped := false
	resolve := func(name string) ([]byte, string, error) {
		content, typ, err := s.resolve(name)
		if name == "model" && !swapped {
			swapped = true
			s.contents["model"] = []byte("model-v2-unreviewed")
		}
		return content, typ, err
	}

	m, err := Freeze("rule", graph, resolve)
	if !errors.Is(err, ErrDigestSwap) {
		t.Fatalf("err = %v, want ErrDigestSwap", err)
	}
	if m != nil {
		t.Fatalf("manifest = %+v, want nil (no mixed version published)", m)
	}
}

// 共享依赖：经两条路径到达同一节点，两次解析都被校验，清单中只有一个条目。
func TestSharedDependencyReverified(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))
	s.put("dict-a", "dict", []byte("dict-a-v1"))
	s.put("dict-b", "dict", []byte("dict-b-v1"))
	s.put("model", "model", []byte("model-v1"))

	graph := map[string]Node{
		"rule":   {Deps: []string{"dict-a", "dict-b"}},
		"dict-a": {Deps: []string{"model"}},
		"dict-b": {Deps: []string{"model"}},
		"model":  {},
	}

	m, err := Freeze("rule", graph, s.resolve)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if got := s.calls["model"]; got != 2 {
		t.Errorf("model resolved %d times, want 2 (re-verified on second path)", got)
	}
	if len(m.Entries) != 4 {
		t.Errorf("entries = %d, want 4 (shared dep appears once)", len(m.Entries))
	}
}

// 最后一项损坏：链式依赖末端的声明摘要与实际内容不符，
// 即使其余节点全部验证通过也不得发布。
func TestLastItemCorruptedRejected(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))
	s.put("dict", "dict", []byte("dict-v1"))
	s.put("model", "model", []byte("model-v1"))

	graph := map[string]Node{
		"rule": {Digest: sha256hex([]byte("rule-v1")), Deps: []string{"dict"}},
		"dict": {Digest: sha256hex([]byte("dict-v1")), Deps: []string{"model"}},
		// 声明的是 model-v1 的摘要，实际内容已被损坏。
		"model": {Digest: sha256hex([]byte("model-v1-expected"))},
	}

	m, err := Freeze("rule", graph, s.resolve)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if m != nil {
		t.Fatalf("manifest = %+v, want nil (no partial publish)", m)
	}
}

// 类型冲突：图中声明的类型与解析得到的类型不一致，拒绝发布。
func TestTypeConflictRejected(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))
	s.put("model", "model", []byte("model-v1"))

	graph := map[string]Node{
		"rule":  {Deps: []string{"model"}},
		"model": {Type: "dict"}, // 声明为词典，实际解析为模型
	}

	m, err := Freeze("rule", graph, s.resolve)
	if !errors.Is(err, ErrTypeConflict) {
		t.Fatalf("err = %v, want ErrTypeConflict", err)
	}
	if m != nil {
		t.Fatalf("manifest = %+v, want nil", m)
	}
}

// 类型在事务内被换掉同样视为冲突。
func TestTypeSwapMidResolutionRejected(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))
	s.put("dict-a", "dict", []byte("dict-a-v1"))
	s.put("dict-b", "dict", []byte("dict-b-v1"))
	s.put("model", "model", []byte("model-v1"))

	graph := map[string]Node{
		"rule":   {Deps: []string{"dict-a", "dict-b"}},
		"dict-a": {Deps: []string{"model"}},
		"dict-b": {Deps: []string{"model"}},
		"model":  {},
	}

	swapped := false
	resolve := func(name string) ([]byte, string, error) {
		content, typ, err := s.resolve(name)
		if name == "model" && !swapped {
			swapped = true
			s.types["model"] = "dict" // 同名引用改指向另一种类型
		}
		return content, typ, err
	}

	m, err := Freeze("rule", graph, resolve)
	if !errors.Is(err, ErrTypeConflict) {
		t.Fatalf("err = %v, want ErrTypeConflict", err)
	}
	if m != nil {
		t.Fatalf("manifest = %+v, want nil", m)
	}
}

// 引用环是允许的：环上节点被重复校验但不会无限递归。
func TestCycleAllowed(t *testing.T) {
	s := newStore()
	s.put("a", "dict", []byte("a-v1"))
	s.put("b", "dict", []byte("b-v1"))

	graph := map[string]Node{
		"a": {Deps: []string{"b"}},
		"b": {Deps: []string{"a"}},
	}

	m, err := Freeze("a", graph, s.resolve)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if len(m.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(m.Entries))
	}
	if s.calls["a"] != 2 || s.calls["b"] != 1 {
		t.Errorf("calls = %v, want root re-verified once via the cycle (a:2, b:1)", s.calls)
	}
}

// 解析失败（名字不存在）不得发布。
func TestResolveFailureRejected(t *testing.T) {
	s := newStore()
	s.put("rule", "rule", []byte("rule-v1"))

	graph := map[string]Node{
		"rule": {Deps: []string{"missing"}},
	}

	m, err := Freeze("rule", graph, s.resolve)
	if !errors.Is(err, ErrResolve) {
		t.Fatalf("err = %v, want ErrResolve", err)
	}
	if m != nil {
		t.Fatalf("manifest = %+v, want nil", m)
	}
}
