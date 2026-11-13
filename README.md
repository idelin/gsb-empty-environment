# COSE加密双方信息

partyU和partyV对调后仍能解开另一场会话的内容。密钥协商的上下文按发送方partyU和接收方partyV固定顺序加入；对调必须失败。顺序与加密时一致的接收方可以解开。可导入的Go库供验证组件调用。只对调双方信息再解密。

本模块提供可导入的 `netsec` 包。密钥派生使用 HKDF-SHA256，内容加密使用
AES-256-GCM。`partyU` 和 `partyV` 分别以带长度边界的 `U`、`V` 字段进入
KDF context，不做排序或反转。

## 基本用法

```go
alice, _ := netsec.GenerateKeyPair()
bob, _ := netsec.GenerateKeyPair()

ciphertext, _ := netsec.Encrypt([]byte("hello"), alice, bob, "alice", "bob")

// 与加密时相同的顺序：成功。
plaintext, _ := netsec.Decrypt(ciphertext, bob, alice, "alice", "bob")

// 只对调 partyU/partyV：返回 netsec.ErrAuthentication。
_, err := netsec.Decrypt(ciphertext, bob, alice, "bob", "alice")
```

也支持预共享密钥、原始 `crypto/ecdh` 密钥和显式双方参数形式：

```go
ciphertext, _ := netsec.Encrypt(plaintext, sharedSecret, "partyU", "partyV")
plaintext, _ := netsec.Decrypt(ciphertext, sharedSecret, "partyU", "partyV")

ciphertext, _ = netsec.EncryptWithParties(partyU, partyV, plaintext, sharedSecret)
```

## 关键接口

- `Encrypt` / `Decrypt`：上下文绑定的认证加密。
- `Seal` / `Open`：同义接口。
- `DeriveKey` / `DeriveKeyWithContext`：直接验证 party 顺序对 KDF 的影响。
- `KDFContext`：包含 `PartyUInfo`（发送方）和 `PartyVInfo`（接收方）。
- `GenerateKeyPair`、`KeyPair`：X25519 密钥生成与 ECDH。
- `NewSession`：固定双方顺序和密钥材料后重复加解密。

验证：

```sh
go test ./...
```
