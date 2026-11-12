# JWE算法组合正交校验

攻击者能控制JWE头里的alg、enc、epk和zip，意图让接收方选弱算法、接受曲线外点或解压炸弹。为Go消息网关实现解密前置校验包，供解密入口调用：alg与enc分别查允许表，ECDH-ES的epk须是P-256有效点，密钥包装只搭配长度匹配的enc，zip一律拒绝，重复头字段拒绝。用组合矩阵、曲线外点和合法密文验证拒绝发生在解密之前。

## 包结构

`netsec.ValidateCompactJWE(token string) error` 是解密入口必须先调用的前置
校验，全程不做任何密钥协商或解密，也不接触私钥材料：

- `doc.go`：包文档与威胁模型。
- `validate.go`：校验实现与可分类的哨兵错误。
- `validate_test.go` / `edge_test.go`：组合矩阵、允许表、曲线外点、zip、
  重复头与结构畸形测试。
- `decrypt_order_test.go`：用真实可解密的 `dir + A256GCM` 密文证明策略拒绝
  发生在任何密码学操作之前。

## 校验策略

- `alg` / `enc` 分别查独立允许表；`RSA1_5`、`none`、PBES 等弱算法不在表内。
- 密钥包装类（`A128/192/256KW`、`ECDH-ES+A128/192/256KW`）包装的密钥字节数
  必须与 `enc` 的 CEK 长度一致（RFC 7518 第 5 节），否则拒绝。
- 任何 ECDH-ES 算法要求 `epk` 为 `kty=EC`、`crv=P-256` 的 JWK，坐标各 32 字节，
  并通过 `crypto/ecdh` 的完整曲线点校验，拒绝曲线外点、无穷远点与私钥分量。
- 出现 `zip` 头一律拒绝（含空值），从源头阻断解压炸弹。
- 用流式 JSON decoder 递归检测任意层级的重复对象键（含 `epk` 与数组内对象）。
- compact 序列化必须恰为 5 段，非空段必须是无填充 base64url；仅 `dir` 与
  直接 `ECDH-ES` 允许空的加密密钥段。

## 接入方式

```go
func HandleJWE(token string, key []byte) ([]byte, error) {
    if err := netsec.ValidateCompactJWE(token); err != nil {
        return nil, err // 策略拒绝，未触碰任何密钥
    }
    return decryptAfterPolicy(token, key)
}
```

错误用 `errors.Is` 分类：`ErrUnsupportedAlg`、`ErrUnsupportedEnc`、
`ErrAlgorithmMismatch`、`ErrEPK`、`ErrCompression`、`ErrDuplicateHeader`、
`ErrMalformedToken`、`ErrMalformedHeader`。

## 验证

```sh
go vet ./...
go test ./... -race -cover
```
