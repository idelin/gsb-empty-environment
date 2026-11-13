# COSE受保护桶

把alg放在未受保护头，或受保护字节与重新编码的结果不一致。alg只从受保护头读取；受保护头必须是确定长度的规范CBOR，重新编码后字节必须相同。受保护头含alg且字节稳定的签名接受。可导入的Go库供验证组件调用。把alg移到未受保护头，并改变CBOR编码长度形式。

## 用法

```go
import netsec "example.org/netsec/q0126"

// 验证组件调用：msg 为 COSE_Sign1 编码字节，pub 为签名者公钥。
payload, err := netsec.VerifySign1(msg, pub, nil)
if err != nil {
    // ErrAlgNotProtected: alg 不在受保护头（例如被移到未受保护头）
    // ErrProtectedNotCanonical: 受保护头非规范CBOR（长度形式被改变、
    //   不定长、键序非规范等），重新编码后字节不一致
    // ErrSignatureMismatch: 签名无效
}
```

## 策略

- `alg`（标签 1）只从受保护头读取；未受保护头中的 `alg` 一律忽略。
- 受保护头必须是确定长度的规范 CBOR：禁止不定长项、禁止非最短长度/整数编码、
  键按规范序排列且无重复；重新编码后字节必须与原始字节完全一致。
- 支持 EdDSA (-8) 与 ES256 (-7)，密钥类型必须与受保护头声明的 alg 匹配。

## 测试

```sh
go test ./...
```

测试覆盖：合法签名接受；`alg` 移到未受保护头拒绝；改变 CBOR 长度形式
（非最短 map 头、非最短整数、不定长 map）拒绝；篡改 payload 拒绝。
