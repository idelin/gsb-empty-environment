package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"syscall"
)

// DefaultMaxDepth 是解释器链的默认最大深度（不含脚本自身）。
const DefaultMaxDepth = 8

var (
	// ErrCycle 表示链中重复出现同一文件对象，构成环。
	ErrCycle = errors.New("netsec: interpreter chain contains a cycle")
	// ErrDepthLimit 表示解释器链超过配置的最大深度。
	ErrDepthLimit = errors.New("netsec: interpreter chain exceeds maximum depth")
	// ErrDenied 表示许可策略拒绝了链中的某个解释器。
	ErrDenied = errors.New("netsec: interpreter rejected by policy")
)

// fileID 用设备号与 inode 唯一标识一个已打开的文件对象。
type fileID struct {
	dev uint64
	ino uint64
}

func identify(info os.FileInfo) (fileID, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, errors.New("netsec: cannot identify file object")
	}
	return fileID{dev: uint64(st.Dev), ino: st.Ino}, nil
}

// Link 是解释器链中的一环：打开对象被固定，内容摘要已固化。
type Link struct {
	Path   string            // 脚本头声明的解释器绝对路径
	Arg    string            // 可选参数
	HasArg bool              // 是否携带参数
	Digest [sha256.Size]byte // 打开对象内容的 SHA-256 摘要
	file   *os.File          // 固定的打开对象
	id     fileID            // 对象身份
}

// File 返回该环固定的打开对象（偏移为 0）。调用方不得关闭它。
func (l *Link) File() *os.File { return l.file }

// Plan 是解析完成的执行计划。它保留原脚本的打开对象，
// 执行时直接复用这些句柄，绝不按路径重新读取。
type Plan struct {
	ScriptPath   string
	ScriptDigest [sha256.Size]byte
	script       *os.File
	chain        []Link
}

// Script 返回原脚本的打开对象（偏移为 0）。调用方不得关闭它。
func (p *Plan) Script() *os.File { return p.script }

// Chain 返回解释器链的副本，顺序为从脚本直达最终二进制。
func (p *Plan) Chain() []Link {
	out := make([]Link, len(p.chain))
	copy(out, p.chain)
	return out
}

// Close 关闭计划保留的所有打开对象。
func (p *Plan) Close() error {
	var first error
	if p.script != nil {
		first = p.script.Close()
		p.script = nil
	}
	for i := range p.chain {
		if err := p.chain[i].file.Close(); err != nil && first == nil {
			first = err
		}
		p.chain[i].file = nil
	}
	return first
}

type config struct {
	maxDepth int
	allow    func(Link) error
}

// Option 调整 Resolve 的行为。
type Option func(*config)

// WithMaxDepth 设置解释器链的最大深度（不含脚本自身），必须为正数。
func WithMaxDepth(n int) Option {
	return func(c *config) { c.maxDepth = n }
}

// WithPolicy 设置许可策略，链中每一环在固定后都会回调一次；
// 返回非 nil 错误即拒绝整条链。
func WithPolicy(fn func(Link) error) Option {
	return func(c *config) { c.allow = fn }
}

// Resolve 从脚本路径出发逐层解析解释器链，返回固定了全部打开
// 对象与内容摘要的执行计划。出错时返回 nil，且不会泄漏句柄。
func Resolve(path string, opts ...Option) (*Plan, error) {
	cfg := config{maxDepth: DefaultMaxDepth}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxDepth < 1 {
		return nil, fmt.Errorf("%w: max depth must be positive", ErrDepthLimit)
	}

	p := &Plan{ScriptPath: path}
	seen := make(map[fileID]struct{})

	fail := func(err error) (*Plan, error) {
		p.Close()
		return nil, err
	}

	// 第一层：原脚本对象，保留在计划中而非按名重读。
	script, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	p.script = script
	id, digest, err := pin(script)
	if err != nil {
		return fail(err)
	}
	p.ScriptDigest = digest
	seen[id] = struct{}{}

	cur := script
	for {
		line, err := readShebangLine(cur)
		if errors.Is(err, errNoShebang) {
			return p, nil // 到达最终二进制解释器
		}
		if err != nil {
			return fail(err)
		}
		interp, arg, hasArg, err := parseShebang(line)
		if err != nil {
			return fail(err)
		}
		if len(p.chain) >= cfg.maxDepth {
			return fail(fmt.Errorf("%w: limit %d", ErrDepthLimit, cfg.maxDepth))
		}

		f, err := os.Open(interp)
		if err != nil {
			return fail(err)
		}
		id, digest, err := pin(f)
		if err != nil {
			f.Close()
			return fail(err)
		}
		if _, dup := seen[id]; dup {
			f.Close()
			return fail(fmt.Errorf("%w: %s", ErrCycle, interp))
		}
		link := Link{Path: interp, Arg: arg, HasArg: hasArg, Digest: digest, file: f, id: id}
		if cfg.allow != nil {
			if err := cfg.allow(link); err != nil {
				f.Close()
				return fail(fmt.Errorf("%w: %s: %v", ErrDenied, interp, err))
			}
		}
		seen[id] = struct{}{}
		p.chain = append(p.chain, link)
		cur = f
	}
}

// pin 固化一个已打开对象：记录对象身份并计算内容摘要，
// 结束后把偏移重置为 0 供后续按对象读取。
func pin(f *os.File) (fileID, [sha256.Size]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return fileID{}, [sha256.Size]byte{}, err
	}
	id, err := identify(info)
	if err != nil {
		return fileID{}, [sha256.Size]byte{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fileID{}, [sha256.Size]byte{}, err
	}
	var h hash.Hash = sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fileID{}, [sha256.Size]byte{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fileID{}, [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return id, digest, nil
}
