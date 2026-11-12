# ML-KEM解封装密文一致性

ML-KEM 解封装靠重新加密后与收到的密文比较，实现里一旦提前返回或报错，攻击者就能用被改动的密文探测私钥。为Go会话服务写供握手处理器调用的解封装包，K-PKE 加密、解密和伪随机函数由调用方注入：先检查密文长度（ML-KEM-768 为 1088 字节）和解封装密钥内嵌的 H(ek) 与 ek 一致，再重新加密并常量时间比较，不一致时返回由 z 与密文经注入函数导出的伪随机密钥而非错误。用单比特翻转、短密文、被改的 H(ek) 和确定性玩具 PKE 验证：翻转密文输出与正常不同但重复调用一致，比较之前不得有按字节早退的路径。

## 用法

握手处理器启动时绑定一次注入的 K-PKE 操作和隐式拒绝 PRF：

```go
dec, err := netsec.NewDecapsulator(kpke, prf)
if err != nil {
	// fail closed
}

sharedKey, err := dec.Decapsulate(decapsulationKey, ciphertext)
```

也可以使用无状态的一次性入口：

```go
sharedKey, err := netsec.Decapsulate(decapsulationKey, ciphertext, kpke, prf)
```

其中 `kpke` 满足 `netsec.KPKEOperations`，`prf` 满足 `netsec.PRF(z, ciphertext) []byte` 并返回 32 字节。

## 安全行为

- 仅 1088 字节的 ML-KEM-768 密文会被处理；长度不合法时返回 `ErrInvalidCiphertextLength`。
- 解封装密钥必须是 2400 字节，且内嵌的 `H(ek)` 必须与 `ek` 匹配；否则返回 `ErrEmbeddedHashMismatch`。
- 结构检查通过后，先解密、再按 `G(m || H(ek))` 重新加密，并用 `crypto/subtle.ConstantTimeCompare` 比较完整密文，不存在按字节提前退出的比较路径。
- 比较失败不会报错，而是返回确定性的 `PRF(z, ciphertext)` 隐式拒绝密钥。成功和失败路径都会调用 PRF，并用常量时间选择输出。

测试覆盖全部 8704 个单比特翻转、短/长密文、被改的 `H(ek)`、错误密钥长度以及确定性玩具 PKE 的重复调用一致性：

```sh
go test ./...
```
