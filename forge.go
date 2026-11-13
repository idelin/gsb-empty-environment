package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
)

// ErrForgeInput 表示伪造输入不合法。
var ErrForgeInput = errors.New("netsec: invalid forgery input")

// Forge 构造拼接伪造消息：
//
//	F = M || (N_first XOR tagM) || N_rest
//
// 其中 M、N 均为整块消息，tagM = CBC-MAC_K(M)。
//
// 正确性：CBC-MAC 处理完 M 后链值为 tagM；下一块输入为
// (N_first XOR tagM)，与链值异或后得到 N_first，恰好等于从零初始
// 链值直接处理 N 的第一块。因此后续链值与 N 完全一致，最终
// MAC(F) = tagN。这证明裸 CBC-MAC 对可变长度消息不安全。
func Forge(M, N, tagM []byte) ([]byte, error) {
	if err := validate(M); err != nil {
		return nil, fmt.Errorf("%w: M: %v", ErrForgeInput, err)
	}
	if err := validate(N); err != nil {
		return nil, fmt.Errorf("%w: N: %v", ErrForgeInput, err)
	}
	if len(tagM) != BlockSize {
		return nil, fmt.Errorf("%w: tagM must be %d bytes", ErrForgeInput, BlockSize)
	}
	if len(M)/BlockSize+len(N)/BlockSize > MaxBlocks {
		return nil, fmt.Errorf("%w: forged message exceeds %d blocks", ErrForgeInput, MaxBlocks)
	}
	f := make([]byte, 0, len(M)+len(N))
	f = append(f, M...)
	first := make([]byte, BlockSize)
	subtle.XORBytes(first, N[:BlockSize], tagM)
	f = append(f, first...)
	f = append(f, N[BlockSize:]...)
	return f, nil
}
