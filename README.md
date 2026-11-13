# ECDH对端公钥点与低阶点校验

攻击者送入不在曲线上或低阶的公钥点，从共享秘密的变化里逐步还原私钥。为Go密钥交换包写可导入的对端公钥校验：P-256 点接受未压缩与压缩编码，压缩点须解出平方根并在曲线上，坐标不得大于等于 p，无穷远点拒绝；X25519 对端 u 值大于等于 2^255-19 拒绝，派生结果全零也拒绝。用不在曲线的点、非规范坐标、低阶 u 和合法点验证。

## 导入与 API

包名 `netsec`，校验失败统一通过 `errors.Is` 匹配哨兵错误：

- `ErrInvalidPublicKey`：编码非法、坐标非规范（`>= p`）、点不在曲线上或无穷远点。
- `ErrSharedSecretZero`：X25519 协商结果为全零（低阶点，私钥贡献失效）。

P-256（`p256.go`）：

- `P256ParsePublicKey(enc)`：接受 SEC1 未压缩（`04||X||Y`，65 字节）与压缩
  （`02/03||X`，33 字节）编码；压缩点按 `y = rhs^((p+1)/4) mod p` 解平方根，
  校验 `y^2 = x^3 - 3x + b` 与奇偶位，再经 `crypto/ecdh` 二次校验。
- `P256SharedSecret(priv, peerEnc)` / `P256ECDH(priv, peer)`。

X25519（`x25519.go`）：

- `X25519ParsePublicKey(enc)`：仅接受 32 字节小端 `u`，且 `u < 2^255 - 19`。
- `X25519SharedSecret(priv, peerEnc)` / `X25519ECDH(priv, peer)`：拒绝全零协商结果。

```go
import "example.org/netsec/q0692"

secret, err := netsec.P256SharedSecret(priv, peerBytes)
if errors.Is(err, netsec.ErrInvalidPublicKey) {
    // 对端公钥非法，终止握手
}

secret, err = netsec.X25519SharedSecret(priv, peerUBytes)
if errors.Is(err, netsec.ErrInvalidPublicKey) || errors.Is(err, netsec.ErrSharedSecretZero) {
    // 非规范 u 或低阶点，终止握手
}
```

测试：`go test ./...`，覆盖不在曲线的点、非规范坐标、无穷远点、混合/截断编码、
低阶 `u`（0、1、p-1）、非规范边界 `u >= p`，以及 RFC 7748 合法向量与双向协商。
