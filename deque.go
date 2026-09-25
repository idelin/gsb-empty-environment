package diskdeque

import (
	"os"
	"path/filepath"
	"sync"
)

// Options 控制磁盘布局。零值由 Open 填充默认值。
type Options struct {
	// BlockCapacity 是单个块最多容纳的元素数，默认 128。
	BlockCapacity int
	// MaxBlocks 是块编号空间大小，块 ID 在 [1, MaxBlocks) 内环绕分配。
	// 分配器保证绝不分配给活跃块或磁盘上仍存在的文件，默认 1<<20。
	MaxBlocks uint64
}

const (
	defaultBlockCapacity = 128
	defaultMaxBlocks     = uint64(1) << 20
)

func (o *Options) normalized() Options {
	cp := Options{BlockCapacity: defaultBlockCapacity, MaxBlocks: defaultMaxBlocks}
	if o != nil {
		if o.BlockCapacity > 0 {
			cp.BlockCapacity = o.BlockCapacity
		}
		if o.MaxBlocks >= 2 {
			cp.MaxBlocks = o.MaxBlocks
		}
	}
	return cp
}

// Deque 是磁盘上的双端队列。同一时刻仅支持单写者（详见 README）。
type Deque struct {
	dir  string
	opts Options

	mu      sync.Mutex
	m       meta
	sizes   []int // 与 m.blocks 平行：每个块的元素数
	crashed bool
	closed  bool
}

// Open 打开（必要时创建）指定目录下的双端队列，并执行崩溃恢复。
func Open(dir string, opts *Options) (*Deque, error) {
	o := opts.normalized()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := recoverDir(dir); err != nil {
		return nil, err
	}

	m := meta{alloc: 1}
	if b, err := os.ReadFile(filepath.Join(dir, metaFile)); err == nil {
		pm, perr := unmarshalMeta(b)
		if perr != nil {
			return nil, perr
		}
		m = *pm
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	sizes := make([]int, len(m.blocks))
	keep := make(map[uint64]bool, len(m.blocks))
	for i, id := range m.blocks {
		n, err := blockElemCount(dir, id)
		if err != nil {
			return nil, err
		}
		sizes[i] = n
		keep[id] = true
	}
	if err := gcBlocks(dir, keep); err != nil {
		return nil, err
	}

	return &Deque{dir: dir, opts: o, m: m, sizes: sizes}, nil
}

// Close 关闭队列句柄。
func (d *Deque) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// Len 返回已提交的元素总数。
func (d *Deque) Len() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.m.count
}

// Head 返回左端（头）所在块 ID；空队列返回 0。
func (d *Deque) Head() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.m.blocks) == 0 {
		return 0
	}
	return d.m.blocks[0]
}

// Tail 返回右端（尾）所在块 ID；空队列返回 0。
// 弹出最后一个元素后 Head()==Tail()==0，头尾一致。
func (d *Deque) Tail() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.m.blocks) == 0 {
		return 0
	}
	return d.m.blocks[len(d.m.blocks)-1]
}

// Begin 开启一个事务。事务可在任意一端混合 push/pop；
// Commit 时两端修改原子生效，Rollback 则全部丢弃。
func (d *Deque) Begin() *Tx {
	d.mu.Lock()
	return &Tx{d: d, tail: len(d.m.blocks)}
}

// ReadRange 返回已提交队列中从逻辑下标 start 开始的 n 个元素（随机区间读取）。
// 读取按块定位，不移动任何磁盘数据。start 超出范围时返回空切片。
func (d *Deque) ReadRange(start, n uint64) ([][]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.crashed || d.closed {
		return nil, ErrClosed
	}
	if n == 0 || start >= d.m.count {
		return nil, nil
	}
	out := make([][]byte, 0, n)
	var idx uint64
	for i, id := range d.m.blocks {
		sz := uint64(d.sizes[i])
		if idx+sz <= start {
			idx += sz
			continue
		}
		elems, err := readBlockFile(d.dir, id)
		if err != nil {
			return nil, err
		}
		for j, e := range elems {
			pos := idx + uint64(j)
			if pos < start {
				continue
			}
			out = append(out, append([]byte(nil), e...))
			if uint64(len(out)) == n {
				return out, nil
			}
		}
		idx += sz
	}
	return out, nil
}
