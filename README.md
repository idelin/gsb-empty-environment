# P-256 门限 ElGamal 解密验证

该包实现加性 ElGamal 密文 `(C1, C2) = (kG, M + kY)` 的门限份额验证与
协调重建，最多支持 16 名成员和门限 8。

## 使用方式

```go
import (
    "math/big"

    netsec "example.org/netsec/q0054"
)

secret, _ := netsec.ScalarFromBig(big.NewInt(123456))
manifest, memberSecrets, _ := netsec.GenerateDealer(
    secret, 3, []uint32{1, 2, 3, 4, 5, 6}, nil,
)

messageScalar, _ := netsec.ScalarFromBig(big.NewInt(42))
message, _ := netsec.ScalarBaseMult(messageScalar)
ciphertext, _ := netsec.Encrypt(manifest, message, nil)

shares := make([]netsec.DecryptionShare, 0, len(memberSecrets))
for _, secretShare := range memberSecrets {
    share, _ := secretShare.Decrypt(manifest, ciphertext, nil)
    shares = append(shares, share)
}

result, err := netsec.Decrypt(manifest, ciphertext, shares, netsec.Budget{})
// err == nil 时 result.Plaintext == message。
```

非法或证明失败的份额会被忽略；重复成员标识最多计一份。协调方必须枚举
所有有效 `t` 子集并得到同一个 `M` 才返回明文。不同子集结果不一致时返回
`*netsec.ConflictError`，可通过 `errors.As` 读取两个子集的成员标识、`D`
和冲突明文。超过 `Budget.MaxSubsets` 时返回 `ErrBudgetExhausted`，且结果中
不含部分明文。

## 编码与校验

- 点只能是 65 字节未压缩 SEC1 编码，必须位于 P-256 上且不是无穷点。
- 标量固定为 32 字节大端序，必须小于 P-256 群阶 `n`。
- 清单标识为非零、互异的 `uint32`，直接作为域元素参与拉格朗日插值。
- `ValidateManifest` 要求 `Y` 等于清单全部公开份额在零点的拉格朗日组合。
- 证明挑战使用固定域标签、清单摘要、密文摘要、成员标识、`Y_i`、`D_i`、
  `A`、`B` 的定长规范编码后做 SHA-256 并模 `n`。

运行测试：

```sh
go test ./...
```
