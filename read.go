package trie

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
)

// VerifyError 表示一个内容寻址引用校验失败或引用目标损坏/缺失。
//
// NodeID 是出问题的节点引用；如果是被父节点引用的子节点，Parent 给出
// 父节点引用，Tag 是它们之间那条边的字节标签，便于定位损坏发生在树的
// 什么位置。Reason 描述具体原因。
type VerifyError struct {
	NodeID NodeID // 损坏或缺失的节点
	Parent NodeID // 引用它的父节点（零值表示它是根或独立校验）
	Tag    *byte  // 父 -> 该节点的边标签
	Reason string
	Got    NodeID // 若为哈希不匹配，这里是实际算出的哈希
}

func newVerifyError(id NodeID, reason string, got NodeID) *VerifyError {
	return &VerifyError{NodeID: id, Reason: reason, Got: got}
}

func (e *VerifyError) Error() string {
	loc := "根节点"
	if !e.Parent.IsZero() {
		tag := "?"
		if e.Tag != nil {
			tag = fmt.Sprintf("0x%02x", *e.Tag)
		}
		loc = fmt.Sprintf("父 %s 经边 %s 引用的子节点", e.Parent.Hex()[:12], tag)
	}
	msg := fmt.Sprintf("trie: 引用校验失败: %s %s: %s", loc, e.NodeID.Hex(), e.Reason)
	if !e.Got.IsZero() {
		msg += "（实际内容哈希 " + e.Got.Hex() + "）"
	}
	return msg
}

// loadNode 从缓存或磁盘加载节点。
func (s *Store) loadNode(id NodeID) (*node, error) {
	if n, ok := s.cache[id]; ok {
		return n, nil
	}
	n, _, err := s.loadNodeFromDisk(id, true)
	return n, err
}

// loadNodeFromDisk 读取节点并用引用哈希验证内容。
// useCache 为 true 时验证成功后写入内存缓存；巡检路径用 false，
// 强制重读磁盘，使"进程运行期间外部损坏"也能被发现。
// 文件缺失或哈希不符都会得到包含 NodeID 的 VerifyError。
func (s *Store) loadNodeFromDisk(id NodeID, useCache bool) (*node, []byte, error) {
	raw, err := ioutil.ReadFile(s.shardPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, newVerifyError(id, "引用目标文件缺失（悬挂引用）", zeroID)
		}
		return nil, nil, fmt.Errorf("trie: 读取节点 %s 失败: %v", id.Hex()[:12], err)
	}
	if got := hashNode(raw); got != id {
		return nil, nil, newVerifyError(id, "内容哈希与引用不一致（节点文件损坏）", got)
	}
	n, err := decodeNode(raw)
	if err != nil {
		return nil, nil, newVerifyError(id, err.Error(), zeroID)
	}
	if useCache {
		s.cache[id] = n
		s.read++
	}
	return n, raw, nil
}

// loadChild 加载子节点并在错误上补充父节点与边标签，使损坏可定位。
func (s *Store) loadChild(parent NodeID, tag byte, id NodeID) (*node, error) {
	n, err := s.loadNode(id)
	if err == nil {
		return n, nil
	}
	if ve, ok := err.(*VerifyError); ok {
		ve.Parent = parent
		t := tag
		ve.Tag = &t
	}
	return nil, err
}

// View 是某个根版本上的只读视图。视图引用的不可变节点不会被任何后续
// 写入改变；但在 Compact 回收掉无标签旧根之后，视图可能读不到节点。
type View struct {
	store *Store
	root  NodeID
}

// Root 返回视图的根引用。
func (v *View) Root() NodeID { return v.root }

// Get 返回 key 的值。空树或键不存在返回 ErrNotFound。
func (v *View) Get(key []byte) ([]byte, error) {
	v.store.mu.RLock()
	defer v.store.mu.RUnlock()

	id := v.root
	var cur *node
	var walked int
	for {
		if id.IsZero() {
			if walked == 0 {
				return nil, ErrEmptyTree
			}
			return nil, ErrNotFound
		}
		var err error
		cur, err = v.store.loadNode(id)
		if err != nil {
			if walked == 0 {
				err = annotateRoot(err)
			}
			return nil, err
		}
		if walked == len(key) {
			if !cur.hasVal {
				return nil, ErrNotFound
			}
			return append([]byte(nil), cur.value...), nil
		}
		child, ok := cur.children[key[walked]]
		if !ok {
			return nil, ErrNotFound
		}
		nxt, err := v.store.loadChild(id, key[walked], child)
		if err != nil {
			return nil, err
		}
		id = child
		cur = nxt
		walked++
	}
}

func annotateRoot(err error) error {
	if ve, ok := err.(*VerifyError); ok {
		ve.Reason = "根引用无法解析: " + ve.Reason
	}
	return err
}

// KV 是扫描结果。
type KV struct {
	Key, Value []byte
}

// ScanPrefix 按字节字典序枚举所有以 prefix 开头的键值；fn 返回 error
// 会中止遍历并原样返回（传 nil prefix 等价于全树扫描，包含空键）。
func (v *View) ScanPrefix(prefix []byte, fn func(KV) error) error {
	v.store.mu.RLock()
	defer v.store.mu.RUnlock()

	id := v.root
	var cur *node
	for i := 0; i < len(prefix); i++ {
		if id.IsZero() {
			return nil // 空树 / 前缀不存在
		}
		var err error
		cur, err = v.store.loadNode(id)
		if err != nil {
			if i == 0 {
				err = annotateRoot(err)
			}
			return err
		}
		child, ok := cur.children[prefix[i]]
		if !ok {
			return nil
		}
		nxt, err := v.store.loadChild(id, prefix[i], child)
		if err != nil {
			return err
		}
		id, cur = child, nxt
	}
	if id.IsZero() {
		return nil
	}
	var err error
	if cur == nil {
		cur, err = v.store.loadNode(id)
		if err != nil {
			return annotateRoot(err)
		}
	}
	return v.walk(id, cur, append([]byte(nil), prefix...), fn)
}

// walk 从 nd 做迭代式深度优先遍历；节点自身的值先于子树输出，
// 子节点按标签升序 => 严格字节字典序（空键 "" 排最前）。
func (v *View) walk(rootID NodeID, rootNode *node, rootKey []byte, fn func(KV) error) error {
	type frame struct {
		id  NodeID
		nd  *node
		key []byte
	}
	st := []frame{{rootID, rootNode, rootKey}}
	for len(st) > 0 {
		f := st[len(st)-1]
		st = st[:len(st)-1]
		if f.nd.hasVal {
			if err := fn(KV{Key: append([]byte(nil), f.key...), Value: append([]byte(nil), f.nd.value...)}); err != nil {
				return err
			}
		}
		tags := make([]int, 0, len(f.nd.children))
		for b := range f.nd.children {
			tags = append(tags, int(b))
		}
		sort.Ints(tags)
		// 逆序压栈，使最小标签先弹出。
		for i := len(tags) - 1; i >= 0; i-- {
			b := byte(tags[i])
			cid := f.nd.children[b]
			cn, err := v.store.loadChild(f.id, b, cid)
			if err != nil {
				return err
			}
			childKey := make([]byte, 0, len(f.key)+1)
			childKey = append(childKey, f.key...)
			childKey = append(childKey, b)
			st = append(st, frame{cid, cn, childKey})
		}
	}
	return nil
}

// ---- Store 级读接口 ----

// CurrentView 返回当前根上的只读视图。
func (s *Store) CurrentView() *View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &View{store: s, root: s.root}
}

// ViewAt 返回指定根引用上的只读视图（用于历史根读取）。
func (s *Store) ViewAt(root NodeID) *View { return &View{store: s, root: root} }

// Get 是 CurrentView().Get 的便捷方法。
func (s *Store) Get(key []byte) ([]byte, error) { return s.CurrentView().Get(key) }

// ScanPrefix 是 CurrentView().ScanPrefix 的便捷方法。
func (s *Store) ScanPrefix(prefix []byte, fn func(KV) error) error {
	return s.CurrentView().ScanPrefix(prefix, fn)
}

// Has 报告键是否存在（不返回值，损坏仍作为 error 暴露）。
func (s *Store) Has(key []byte) (bool, error) {
	_, err := s.Get(key)
	if err == ErrNotFound || err == ErrEmptyTree {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// jsonMarshalIndent 集中一处，便于磁盘格式保持稳定可读。
func jsonMarshalIndent(m diskManifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LeafNodeID 返回 key 终止节点的内容引用（供巡检/演示/测试定位使用）；
// 键不存在时返回零引用与错误。
func LeafNodeID(v *View, key []byte) (NodeID, error) {
	v.store.mu.RLock()
	defer v.store.mu.RUnlock()
	id := v.root
	for i := 0; i < len(key); i++ {
		n, err := v.store.loadNode(id)
		if err != nil {
			return zeroID, err
		}
		next, ok := n.children[key[i]]
		if !ok {
			return zeroID, ErrNotFound
		}
		id = next
	}
	return id, nil
}
