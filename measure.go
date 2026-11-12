package netsec

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
)

// VersionProof 是对象的稳定版本证明。
//
// Seq 是对象内单调递增的版本号；Nonce 是每次版本更替生成的唯一世代
// 标识，即使底层存储把 Seq 回绕或复用，Nonce 也不会重复。相等判定必须
// 同时比较三者，仅比较 Seq 无法识别版本号复用攻击。重命名不改变对象
// 内容与版本，因此重命名前后 VersionProof 保持不变。
type VersionProof struct {
	ObjectID string
	Seq      uint64
	Nonce    string
}

// Equal 判断两个版本证明是否指向同一对象的同一版本。
func (p VersionProof) Equal(other VersionProof) bool {
	return p.ObjectID == other.ObjectID && p.Seq == other.Seq && p.Nonce == other.Nonce
}

// String 返回便于日志记录的证明表示。
func (p VersionProof) String() string {
	return p.ObjectID + "#" + itoa(p.Seq) + ":" + p.Nonce
}

// View 是对象某一版本的不可变视图。
//
// 视图在打开时对内容做快照，之后对象本身被修改、替换或重命名都不会
// 影响视图字节，Proof 始终返回打开时刻固定下来的版本证明。执行回调
// 只能消费这个视图，不允许再按路径重新打开对象。
type View interface {
	io.Reader
	io.Seeker
	io.Closer

	// Proof 返回视图打开时刻固定的版本证明。
	Proof() VersionProof
	// Size 返回不可变快照的字节长度。
	Size() int64
}

// Object 是启动宿主持有的只读对象句柄。
//
// 句柄由宿主在初始化时按路径打开一次，之后测量与执行全程只通过句柄
// 访问，组件内部不会出现任何按路径打开的动作。
type Object interface {
	// ID 返回跨重命名稳定的对象标识。
	ID() string
	// Proof 返回对象当前的版本证明。
	Proof(ctx context.Context) (VersionProof, error)
	// OpenView 基于句柄（而非路径）打开当前版本的不可变视图。
	OpenView(ctx context.Context) (View, error)
}

// ExecuteFunc 是执行回调。
//
// view 是测量通过的同一版本不可变视图，measured 是组件对该视图算出的
// 摘要。回调必须实际消费整个视图，并返回自己对实际消费字节算出的摘要
// 与字节数；组件会据此核对回调确实消费了被测版本，而不是短路返回或
// 读取了其它内容。
type ExecuteFunc func(ctx context.Context, view View, measured []byte) (consumedDigest []byte, consumedBytes int64, err error)

var (
	// ErrAnchorMismatch 表示调用方要求的稳定版本证明与对象当前版本不一致。
	ErrAnchorMismatch = errors.New("netsec: anchor version proof does not match the object")
	// ErrVersionChanged 表示测量窗口内对象版本发生了变化，需要重试。
	ErrVersionChanged = errors.New("netsec: object version changed inside the measurement window")
	// ErrExhausted 表示对象在测量窗口内持续变化，重试次数耗尽。
	ErrExhausted = errors.New("netsec: measurement retries exhausted while the object kept changing")
	// ErrDigestMismatch 表示回调没有完整消费被测的不可变视图，或消费摘要与测量摘要不符。
	ErrDigestMismatch = errors.New("netsec: callback did not consume the exact measured immutable view")
)

// Option 配置 MeasureAndExecute 的行为。
type Option func(*config)

type config struct {
	hash        crypto.Hash
	maxAttempts int
}

// WithHash 选择测量摘要算法，默认 SHA-256。
func WithHash(h crypto.Hash) Option {
	return func(c *config) { c.hash = h }
}

// WithMaxAttempts 设置最大测量尝试次数（含首次），默认 8。
func WithMaxAttempts(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxAttempts = n
		}
	}
}

// Result 是一次成功的测量执行结果。
type Result struct {
	// Proof 是实际执行版本的版本证明。
	Proof VersionProof
	// Digest 是该版本不可变视图的测量摘要。
	Digest []byte
	// Attempts 是总尝试次数，1 表示首个测量窗口即稳定。
	Attempts int
}

// MeasureAndExecute 对只读对象句柄执行“先测量、后执行”。
//
// anchor 为调用方持有的稳定版本证明：非 nil 时只允许执行该精确版本，
// 对象已切换到其它版本（含版本号复用）立即返回 ErrAnchorMismatch；
// 为 nil 时执行测量窗口内保持稳定的当前版本。
//
// 每轮尝试先取版本证明，再基于同一句柄打开不可变视图并测量；若打开
// 时刻或测量复核时刻版本证明发生变化，则丢弃该视图并重试。只有测量
// 前后证明一致的视图才会交给 execute，且回调消费的摘要必须与测量摘要
// 完全一致。整个过程不按路径重新打开对象。
func MeasureAndExecute(ctx context.Context, obj Object, anchor *VersionProof, execute ExecuteFunc, opts ...Option) (*Result, error) {
	cfg := config{hash: crypto.SHA256, maxAttempts: 8}
	for _, opt := range opts {
		opt(&cfg)
	}
	if !cfg.hash.Available() {
		return nil, errors.New("netsec: requested hash function is not available")
	}

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		current, err := obj.Proof(ctx)
		if err != nil {
			return nil, err
		}
		if anchor != nil && !current.Equal(*anchor) {
			return nil, ErrAnchorMismatch
		}

		view, err := obj.OpenView(ctx)
		if err != nil {
			return nil, err
		}

		// 取证明与打开视图之间对象可能已被改写：视图必须恰好对应当前证明。
		if !view.Proof().Equal(current) {
			_ = view.Close()
			if attempt >= cfg.maxAttempts {
				return nil, ErrExhausted
			}
			continue
		}

		size := view.Size()
		if _, err := view.Seek(0, io.SeekStart); err != nil {
			_ = view.Close()
			return nil, err
		}
		hasher := newHasher(cfg.hash)
		if _, err := io.Copy(hasher, view); err != nil {
			_ = view.Close()
			return nil, err
		}
		measured := hasher.Sum(nil)

		// 测量完成后复核：窗口内版本一旦变化，本视图作废并重试。
		after, err := obj.Proof(ctx)
		if err != nil {
			_ = view.Close()
			return nil, err
		}
		if !after.Equal(current) {
			_ = view.Close()
			if attempt >= cfg.maxAttempts {
				return nil, ErrExhausted
			}
			continue
		}

		// 测量窗口稳定：把同一不可变视图倒回起点交给回调消费。
		if _, err := view.Seek(0, io.SeekStart); err != nil {
			_ = view.Close()
			return nil, err
		}
		consumed, consumedBytes, execErr := execute(ctx, view, measured)
		_ = view.Close()
		if execErr != nil {
			return nil, execErr
		}
		if consumedBytes != size || !bytes.Equal(consumed, measured) {
			return nil, ErrDigestMismatch
		}

		return &Result{Proof: current, Digest: measured, Attempts: attempt}, nil
	}
}

func newHasher(h crypto.Hash) hash.Hash {
	if h == crypto.SHA256 {
		return sha256.New()
	}
	return h.New()
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
