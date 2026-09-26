package gsb

import "encoding/binary"

// Span 指向某物理块内的一段字节，是逻辑序列的最小引用单元。
type Span struct {
	Block uint64
	Off   int
	Len   int
}

// Version 是一个分支版本：一串不可变跨度。编辑时整体替换（写时复制），
// 旧版本持有的跨度与底块不受任何影响。
type Version struct {
	spans  []Span
	length int
}

// Repo 是分支仓库：一个物理块存储 + 若干命名分支。
type Repo struct {
	store    *Store
	branches map[string]*Version
}

// NewRepo 创建仓库并附带空的 main 分支，quota 为物理字节配额（0 = 不限）。
func NewRepo(quota int) *Repo {
	return &Repo{
		store:    NewStore(quota),
		branches: map[string]*Version{"main": {}},
	}
}

// Store 返回底层物理块存储（账本查询入口）。
func (r *Repo) Store() *Store { return r.store }

// Begin 开启一个事务；事务内可组合多个编辑，Commit 生效、Rollback 回滚。
func (r *Repo) Begin() *Tx { return &Tx{store: r.store} }

// Branches 返回当前分支名列表（顺序不定）。
func (r *Repo) Branches() []string {
	names := make([]string, 0, len(r.branches))
	for name := range r.branches {
		names = append(names, name)
	}
	return names
}

func (r *Repo) version(name string) (*Version, error) {
	v := r.branches[name]
	if v == nil {
		return nil, ErrNoBranch
	}
	return v, nil
}

// Len 返回分支的逻辑字节长度。
func (r *Repo) Len(branch string) (int, error) {
	v, err := r.version(branch)
	if err != nil {
		return 0, err
	}
	return v.length, nil
}

// cutSpans 截取跨度序列中逻辑区间 [lo, hi) 对应的子跨度，纯跨度运算，零块复制。
func cutSpans(spans []Span, lo, hi int) []Span {
	var out []Span
	pos := 0
	for _, s := range spans {
		sLo, sHi := pos, pos+s.Len
		pos = sHi
		a, b := max(lo, sLo), min(hi, sHi)
		if a < b {
			out = append(out, Span{Block: s.Block, Off: s.Off + (a - sLo), Len: b - a})
		}
	}
	return out
}

// Splice 是唯一的编辑原语：用 data 替换 branch 中 [off, off+remove) 区间。
// 头尾未触及部分以跨度切割共享底块（零复制），仅新数据分配物理块。
// 整个操作在 tx 内原子完成，出错或回滚后旧版本原样保留。
func (r *Repo) Splice(tx *Tx, branch string, off, remove int, data []byte) error {
	if tx.closed {
		return ErrTxClosed
	}
	v, err := r.version(branch)
	if err != nil {
		return err
	}
	if off < 0 || remove < 0 || off+remove > v.length {
		return ErrRange
	}
	head := cutSpans(v.spans, 0, off)
	tail := cutSpans(v.spans, off+remove, v.length)
	mid := make([]Span, 0, (len(data)+BlockSize-1)/BlockSize)
	rest := data
	for len(rest) > 0 {
		n := min(BlockSize, len(rest))
		id, err := tx.alloc(rest[:n])
		if err != nil {
			return err
		}
		mid = append(mid, Span{Block: id, Off: 0, Len: n})
		rest = rest[n:]
	}
	for _, s := range head {
		tx.retain(s.Block)
	}
	for _, s := range tail {
		tx.retain(s.Block)
	}
	out := make([]Span, 0, len(head)+len(mid)+len(tail))
	out = append(out, head...)
	out = append(out, mid...)
	out = append(out, tail...)
	oldSpans, oldLen := v.spans, v.length
	for _, s := range oldSpans {
		tx.release(s.Block)
	}
	v.spans = out
	v.length = oldLen - remove + len(data)
	tx.undos = append(tx.undos, func() { v.spans, v.length = oldSpans, oldLen })
	return nil
}

// auto 把单个操作包进事务：失败（含配额超限）自动整体回滚。
func (r *Repo) auto(op func(tx *Tx) error) error {
	tx := r.Begin()
	if err := op(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Insert 在 off 处插入 data（off 可等于长度，即追加）。
func (r *Repo) Insert(branch string, off int, data []byte) error {
	return r.auto(func(tx *Tx) error { return r.Splice(tx, branch, off, 0, data) })
}

// Overwrite 用 data 覆盖 [off, off+len(data))，要求区间已存在（不延长序列）。
func (r *Repo) Overwrite(branch string, off int, data []byte) error {
	return r.auto(func(tx *Tx) error { return r.Splice(tx, branch, off, len(data), data) })
}

// Delete 删除 [off, off+n)。
func (r *Repo) Delete(branch string, off, n int) error {
	return r.auto(func(tx *Tx) error { return r.Splice(tx, branch, off, n, nil) })
}

// MoveTx 把 [src, src+n) 移动到 dst（操作前坐标系）。
// 源字节在任何修改之前读出，因此源与目标重叠时右值仍按操作前内容读取。
// dst 落入源区间内部时，插入点钳制到删除后序列的合法范围 [0, L-n]。
func (r *Repo) MoveTx(tx *Tx, branch string, src, dst, n int) error {
	v, err := r.version(branch)
	if err != nil {
		return err
	}
	L := v.length
	if src < 0 || n < 0 || src+n > L || dst < 0 || dst > L {
		return ErrRange
	}
	if n == 0 {
		return nil
	}
	buf, err := r.Read(branch, src, n) // 操作前内容快照
	if err != nil {
		return err
	}
	if err := r.Splice(tx, branch, src, n, nil); err != nil {
		return err
	}
	ins := dst
	if dst > src {
		ins = dst - n
	}
	if ins < 0 {
		ins = 0
	}
	if ins > L-n {
		ins = L - n
	}
	return r.Splice(tx, branch, ins, 0, buf)
}

// Move 是 MoveTx 的自动事务版本。
func (r *Repo) Move(branch string, src, dst, n int) error {
	return r.auto(func(tx *Tx) error { return r.MoveTx(tx, branch, src, dst, n) })
}

// SnapshotTx 把 src 分支快照为 dst 分支：只复制跨度表并增加引用计数，
// 零块复制；此后两个分支的相交修改互不影响。
func (r *Repo) SnapshotTx(tx *Tx, dst, src string) error {
	if tx.closed {
		return ErrTxClosed
	}
	sv, err := r.version(src)
	if err != nil {
		return err
	}
	spans := append([]Span(nil), sv.spans...)
	for _, s := range spans {
		tx.retain(s.Block)
	}
	old, had := r.branches[dst]
	if had {
		for _, s := range old.spans {
			tx.release(s.Block)
		}
	}
	r.branches[dst] = &Version{spans: spans, length: sv.length}
	tx.undos = append(tx.undos, func() {
		if had {
			r.branches[dst] = old
		} else {
			delete(r.branches, dst)
		}
	})
	return nil
}

// Snapshot 是 SnapshotTx 的自动事务版本。
func (r *Repo) Snapshot(dst, src string) error {
	return r.auto(func(tx *Tx) error { return r.SnapshotTx(tx, dst, src) })
}

// DropBranchTx 删除分支并释放其持有的底块引用。
func (r *Repo) DropBranchTx(tx *Tx, name string) error {
	if tx.closed {
		return ErrTxClosed
	}
	v, err := r.version(name)
	if err != nil {
		return err
	}
	for _, s := range v.spans {
		tx.release(s.Block)
	}
	delete(r.branches, name)
	tx.undos = append(tx.undos, func() { r.branches[name] = v })
	return nil
}

// DropBranch 是 DropBranchTx 的自动事务版本。
func (r *Repo) DropBranch(name string) error {
	return r.auto(func(tx *Tx) error { return r.DropBranchTx(tx, name) })
}

// Read 跨块读取 [off, off+n)，返回新分配的拷贝。
func (r *Repo) Read(branch string, off, n int) ([]byte, error) {
	v, err := r.version(branch)
	if err != nil {
		return nil, err
	}
	if off < 0 || n < 0 || off+n > v.length {
		return nil, ErrRange
	}
	out := make([]byte, 0, n)
	for _, s := range cutSpans(v.spans, off, off+n) {
		b := r.store.blocks[s.Block]
		out = append(out, b.data[s.Off:s.Off+s.Len]...)
	}
	return out, nil
}

// ReadAll 读取分支全部内容。
func (r *Repo) ReadAll(branch string) ([]byte, error) {
	v, err := r.version(branch)
	if err != nil {
		return nil, err
	}
	return r.Read(branch, 0, v.length)
}

// ReadUint64LE 跨块读取小端 uint64。
func (r *Repo) ReadUint64LE(branch string, off int) (uint64, error) {
	b, err := r.Read(branch, off, 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

// ReadUint64BE 跨块读取大端 uint64。
func (r *Repo) ReadUint64BE(branch string, off int) (uint64, error) {
	b, err := r.Read(branch, off, 8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

// ReadUint32LE 跨块读取小端 uint32。
func (r *Repo) ReadUint32LE(branch string, off int) (uint32, error) {
	b, err := r.Read(branch, off, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// ReadUint32BE 跨块读取大端 uint32。
func (r *Repo) ReadUint32BE(branch string, off int) (uint32, error) {
	b, err := r.Read(branch, off, 4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}
