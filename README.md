# EVM 属性 HMAC 与 inode 绑定评估

供文件访问前调用的 Go 评估包。它验证 `security.evm` 是否仍绑定在当前文件的
`security.ima`、`security.selinux`、inode、`i_generation`、uid、gid 和 mode 上。

## 安全语义

| 场景 | 类型 2（本机 HMAC） | 类型 5（可移植签名） |
| --- | --- | --- |
| 属性、uid、gid 或 mode 被改 | 拒绝 | 拒绝 |
| 文件被复制到另一 inode / generation | 拒绝 | 允许（可移植语义） |
| 硬链接到另一路径，inode 相同 | 允许 | 允许 |
| 所需密钥未加载 | 拒绝 | 拒绝 |
| 类型字节未知或载荷长度错误 | 拒绝 | 拒绝 |

评估器是 fail-closed 的。缺少 HMAC 密钥或受信公钥时不会退化为只检查
`security.ima`；零值 `Result` 也不是允许。HMAC 校验失败只能证明某个被绑定字段
发生了变化；单个 HMAC 标签本身无法反推出究竟是 uid 变了还是 inode 变了。

## 载荷格式

两个属性值都使用 `u32le 长度 || 值` 编码，避免拼接歧义。

类型 2（`0x02`，HMAC-SHA256）：

```text
security.evm = 0x02 || HMAC-SHA256(key,
    len(ima) || ima ||
    len(selinux) || selinux ||
    u64le(inode) || u32le(i_generation) ||
    u32le(uid) || u32le(gid) || u32le(mode))
```

类型 5（`0x05`，Ed25519 可移植签名）：

```text
security.evm = 0x05 || Ed25519-Sign(priv,
    len(ima) || ima ||
    len(selinux) || selinux ||
    u32le(uid) || u32le(gid) || u32le(mode))
```

类型 5 有意不包含 inode 与 `i_generation`。本包使用 `ed25519.PublicKey` 作为
受信锚点，并依次尝试已加载的受信公钥。

## 使用

调用方应在真正放行访问前读取当前 inode 元数据和 `security.evm`，避免先检查
再按路径重新打开的 TOCTOU 窗口；较稳妥的做法是先打开文件，再对同一 fd 执行
`fstat` / `fgetxattr`。

```go
evaluator, err := netsec.NewEvaluator(netsec.Config{
    HMACKey:           hostKey,
    TrustedPublicKeys: []ed25519.PublicKey{portableKey},
})
if err != nil {
    return err
}

result := evaluator.Evaluate(netsec.FileAttributes{
    IMA:        imaValue,
    SELinux:    selinuxValue,
    Inode:      uint64(stat.Ino),
    Generation: stat.Gen,
    UID:        stat.Uid,
    GID:        stat.Gid,
    Mode:       stat.Mode, // 原始 Unix st_mode，不是 os.FileMode
}, evmXattr)
if !result.Allowed() {
    return result.Err()
}
```

`NewHMACXattr` 与 `SignPortableXattr` 可用于测试或离线配置工具，不应在访问路径上
用它们绕过校验。密钥可在运行时通过 `SetHMACKey` 和 `AddTrustedPublicKey` 加载；
清空 HMAC 密钥会立即恢复 fail-closed 行为。

## 验证

测试覆盖改 uid、改 IMA/SELinux/gid/mode、把 HMAC 复制到另一 inode、同 inode
硬链接、缺 HMAC 密钥、缺签名受信公钥、未知类型、畸形载荷、错误密钥，以及可移植
签名跨 inode/generation 仍有效而改属性后失效。

```sh
go test ./...
go test -race ./...
```
