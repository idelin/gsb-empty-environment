# 解密成功但密钥身份不唯一

存储层只靠AEAD标签判断解密成功，同一密文若在错误密钥下也被接受，会把对象归到错误租户。给Go存储宿主实现带密钥承诺的封装包，AEAD由宿主注入；每个密钥有稳定keyID和32字节原始密钥，封装随机nonce并计算ciphertext=AEAD.Seal(key,nonce,plaintext,aad)，commitKey=HMAC-SHA256(key,固定承诺派生标签)，commit=HMAC-SHA256(commitKey,keyID长度及字节||nonce长度及字节||aad长度及字节||ciphertext)的前16字节。记录规范编码版本、keyID、nonce、commit和ciphertext，长度有上限。解封先按keyID从宿主只读密钥环取唯一密钥，记录中的版本、keyID和长度也纳入commit输入；常量时间验证commit后才调用AEAD.Open；未知或重复keyID、承诺失败、AEAD失败均返回相同外部错误且不交付明文。密钥轮换允许多个keyID，但重加密先完整解封旧记录再用新ID封装并原子发布，失败保留旧记录。用故意接受任意密钥的坏AEAD替身证明承诺仍阻止跨keyID解密，再测改keyID、nonce、AAD、密文、承诺及轮换中断；明确本题承诺依赖HMAC假设，不能把它说成底层AEAD天然key-committing。

## 实现概览

包路径为 `example.org/netsec/q0063`（包名 `netsec`）。核心 API：

- `NewKey(id, material)`：校验稳定 keyID 与 32 字节原始密钥。
- `NewStaticKeyRing(keys...)`：不可变只读密钥环；重复 keyID 不会被折叠成唯一密钥。
- `Envelope.Seal / Open / SealBytes / OpenBytes`：封装与解封；AEAD 由宿主通过 `AEADFactory` 注入。
- `Envelope.Reencrypt / Rotate`：完整解封旧记录、用新 keyID 封装，再经 `AtomicRecordStore.CompareAndSwap` 原子发布。
- `MemoryRecordStore`：线程安全的内存 CAS 存储，便于测试或小型宿主。

宿主提供 AEAD 工厂，例如 AES-GCM：

```go
factory := netsec.AEADFactoryFunc(func(key []byte) (cipher.AEAD, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, err
    }
    return cipher.NewGCM(block)
})

envelope, err := netsec.NewEnvelope(factory, ring)
record, err := envelope.Seal("tenant-a:v1", plaintext, aad)
plaintext, err := envelope.Open(record, aad)
```

## 承诺与记录格式

固定派生标签为 `example.org/netsec/q0063/key-commit/v1`：

```text
commitKey = HMAC-SHA256(rawKey, commitment-label)
commit    = first16(HMAC-SHA256(commitKey, committed-input))

committed-input =
    version(1 byte)
 || u32be(len(keyID)) || keyID
 || u32be(len(nonce)) || nonce
 || u32be(len(aad)) || aad
 || u32be(len(ciphertext)) || ciphertext
```

规范记录编码（所有多字节长度均为大端）：

```text
version             1 byte   (当前仅 1)
keyID length        1 byte
nonce length        1 byte
ciphertext length   4 bytes
commit             16 bytes
keyID               keyID length bytes
nonce               nonce length bytes
ciphertext          ciphertext length bytes
```

上限：`keyID` 255 字节、`nonce` 64 字节、`ciphertext` 64 MiB、`AAD` 1 MiB、记录总长对应为 64 MiB 加头部与 keyID/nonce 上限。解析拒绝未知版本、零长度 keyID/nonce、超限字段、截断与尾随数据。

解封顺序是：解析并校验记录 → 按 keyID 取唯一密钥 → 用记录版本、keyID、nonce、外部 AAD、密文及长度重算承诺 → `crypto/subtle.ConstantTimeCompare` 常量时间校验 → 校验 nonce/密文尺寸 → 调用宿主 `AEAD.Open`。未知或重复 keyID、记录畸形、承诺失败、AEAD 构造/解封失败一律返回同一个 `ErrOpen`，且返回 `nil` 明文；AEAD 即使“失败同时返回部分明文”，也会先擦除再丢弃。

## 轮换语义

`Reencrypt` 只在旧记录通过完整承诺校验和 AEAD 解封后才生成新记录。发布使用带旧记录比较的 `CompareAndSwap`：并发写导致比较失败时返回 `ErrStoreConflict`，存储错误原样返回，二者都不会替换旧记录；未知/重复新 keyID 或封装失败同样保留旧记录。生产存储需要实现真正的原子 CAS，`MemoryRecordStore` 仅提供等价的内存语义。

## 安全边界

本实现的密钥承诺安全性依赖 HMAC-SHA256 的 PRF/抗碰撞假设，以及 32 字节原始密钥的保密性；截断到 16 字节提供 128 位承诺强度。它**不是**底层 AEAD 天然具备 key-committing 性质：即使注入的 AEAD 故意接受任意密钥解密，独立承诺仍会阻止跨 keyID 解封。测试 `TestCommitmentBlocksCrossKeyIDOpenWithBadAEAD` 正是用这种坏 AEAD 替身验证该点；其余测试覆盖改 keyID、nonce、AAD、密文、承诺、版本，以及承诺先于 `AEAD.Open`、轮换失败保留旧记录。
