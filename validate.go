package netsec

import (
	"errors"
	"fmt"
	"math"
)

const (
	// RequiredRingSize 是每个交易输入环成员（混合数）的固定数量。
	RequiredRingSize = 16

	// NormalUnlockBlocks 是普通输出在链上成熟所需的区块确认数。
	NormalUnlockBlocks = 10

	// CoinbaseUnlockBlocks 是 coinbase 输出在链上成熟所需的区块确认数。
	CoinbaseUnlockBlocks = 60
)

// KeyImage 是一次性输出对应的密钥映像，用于防止双花。
type KeyImage [32]byte

// OutputIndex 是输出在全局输出表中的绝对索引。
type OutputIndex uint64

// OutputMeta 描述链上一个输出的准入相关元数据。
type OutputMeta struct {
	Index       OutputIndex
	BlockHeight uint64
	Coinbase    bool
}

// TxInput 是待校验的交易输入，KeyOffsets 使用相对偏移编码。
type TxInput struct {
	KeyImage   KeyImage
	KeyOffsets []uint64
}

// Transaction 是待准入校验的交易。
type Transaction struct {
	Inputs []TxInput
}

// GroupOrderChecker 由调用方注入，校验密钥映像是否落在椭圆曲线群阶之内。
type GroupOrderChecker func(image KeyImage) bool

// RingSignatureVerifier 由调用方注入，校验输入的环签名是否成立。
type RingSignatureVerifier func(input *TxInput, ring []OutputMeta) bool

// ChainState 抽象链上与内存池状态，由节点侧实现并注入。
type ChainState interface {
	// CurrentHeight 返回当前链尖高度。
	CurrentHeight() uint64
	// GetOutput 按全局绝对索引返回链上已存在的输出。
	GetOutput(index OutputIndex) (meta OutputMeta, ok bool)
	// IsSpent 判断密钥映像是否已出现在已花费集合中。
	IsSpent(image KeyImage) bool
	// MempoolHasKeyImage 判断内存池中是否已存在引用该密钥映像的交易。
	MempoolHasKeyImage(image KeyImage) bool
}

var (
	ErrNoInputs              = errors.New("transaction has no inputs")
	ErrRingSize              = errors.New("ring size must equal 16")
	ErrOffsetOverflow        = errors.New("key offset accumulation overflows")
	ErrDuplicateRingMember   = errors.New("ring members must be strictly increasing with no duplicates")
	ErrOutputNotFound        = errors.New("ring member output does not exist on chain")
	ErrOutputLocked          = errors.New("ring member output is not yet unlocked")
	ErrKeyImageGroupOrder    = errors.New("key image failed group order check")
	ErrKeyImageSpent         = errors.New("key image already exists in spent set")
	ErrKeyImageDuplicateInTx = errors.New("key image duplicated within the same transaction")
	ErrKeyImageInMempool     = errors.New("key image already referenced by a mempool transaction")
	ErrRingSignature         = errors.New("ring signature verification failed")
	ErrNilDependency         = errors.New("validator dependency must not be nil")
)

// Validator 执行交易输入准入校验。环签名与群阶检查通过依赖注入完成。
type Validator struct {
	chain     ChainState
	order     GroupOrderChecker
	ringCheck RingSignatureVerifier
}

// NewValidator 构造输入校验器，三个依赖均不可为 nil。
func NewValidator(chain ChainState, order GroupOrderChecker, ringCheck RingSignatureVerifier) (*Validator, error) {
	if chain == nil {
		return nil, fmt.Errorf("%w: chain state", ErrNilDependency)
	}
	if order == nil {
		return nil, fmt.Errorf("%w: group order checker", ErrNilDependency)
	}
	if ringCheck == nil {
		return nil, fmt.Errorf("%w: ring signature verifier", ErrNilDependency)
	}
	return &Validator{chain: chain, order: order, ringCheck: ringCheck}, nil
}

// resolveAbsoluteOffsets 将相对偏移还原为全局绝对索引。
// 首个偏移本身即绝对索引；其后任一偏移为 0 都会导致环成员重复，
// 累加溢出同样被拒绝。返回序列保证严格递增且无重复。
func resolveAbsoluteOffsets(offsets []uint64) ([]OutputIndex, error) {
	absolute := make([]OutputIndex, len(offsets))
	var accumulated uint64
	for i, offset := range offsets {
		if i > 0 && offset == 0 {
			return nil, fmt.Errorf("%w: offset at position %d is zero", ErrDuplicateRingMember, i)
		}
		if offset > math.MaxUint64-accumulated {
			return nil, fmt.Errorf("%w: at position %d", ErrOffsetOverflow, i)
		}
		accumulated += offset
		absolute[i] = OutputIndex(accumulated)
	}
	return absolute, nil
}

// isUnlocked 判断输出在给定链尖高度下是否已度过锁定期。
// 普通输出需 10 个区块确认，coinbase 输出需 60 个区块确认。
func isUnlocked(meta OutputMeta, currentHeight uint64) bool {
	threshold := uint64(NormalUnlockBlocks)
	if meta.Coinbase {
		threshold = CoinbaseUnlockBlocks
	}
	if meta.BlockHeight > math.MaxUint64-threshold {
		return false
	}
	return meta.BlockHeight+threshold <= currentHeight
}

// Validate 对交易的全部输入依次执行准入校验，任一输入失败即返回
// 包裹对应哨兵错误的 error；全部通过时返回 nil。
func (v *Validator) Validate(tx *Transaction) error {
	if tx == nil || len(tx.Inputs) == 0 {
		return ErrNoInputs
	}

	currentHeight := v.chain.CurrentHeight()
	seenKeyImages := make(map[KeyImage]struct{}, len(tx.Inputs))

	for i := range tx.Inputs {
		input := &tx.Inputs[i]

		if len(input.KeyOffsets) != RequiredRingSize {
			return fmt.Errorf("input %d: %w: got %d", i, ErrRingSize, len(input.KeyOffsets))
		}

		absoluteIndices, err := resolveAbsoluteOffsets(input.KeyOffsets)
		if err != nil {
			return fmt.Errorf("input %d: %w", i, err)
		}

		ring := make([]OutputMeta, 0, RequiredRingSize)
		for _, index := range absoluteIndices {
			meta, ok := v.chain.GetOutput(index)
			if !ok {
				return fmt.Errorf("input %d: %w: output index %d", i, ErrOutputNotFound, index)
			}
			if !isUnlocked(meta, currentHeight) {
				return fmt.Errorf("input %d: %w: output index %d at height %d", i, ErrOutputLocked, index, meta.BlockHeight)
			}
			ring = append(ring, meta)
		}

		if !v.order(input.KeyImage) {
			return fmt.Errorf("input %d: %w", i, ErrKeyImageGroupOrder)
		}

		if _, duplicated := seenKeyImages[input.KeyImage]; duplicated {
			return fmt.Errorf("input %d: %w", i, ErrKeyImageDuplicateInTx)
		}
		seenKeyImages[input.KeyImage] = struct{}{}

		if v.chain.IsSpent(input.KeyImage) {
			return fmt.Errorf("input %d: %w", i, ErrKeyImageSpent)
		}

		if v.chain.MempoolHasKeyImage(input.KeyImage) {
			return fmt.Errorf("input %d: %w", i, ErrKeyImageInMempool)
		}

		if !v.ringCheck(input, ring) {
			return fmt.Errorf("input %d: %w", i, ErrRingSignature)
		}
	}

	return nil
}
