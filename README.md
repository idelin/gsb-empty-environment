# EVM 属性 HMAC 与 inode 绑定评估

该包在文件访问前验证 `security.evm`，保护 `security.ima`、
`security.selinux`、uid、gid 和 mode。判定默认拒绝：任何读取错误、缺钥、
EVM 缺失/畸形、未知类型字节或密码学校验失败都会阻止访问，且不会退化为
只检查 `security.ima`。

## 使用

```go
evaluator := netsec.NewEvaluator(hostHMACKey, trustedPublicKeys...)

state, err := netsec.ReadState("/path/to/file")
if err != nil {
    // fail closed
}
result := evaluator.Evaluate(state)
if !result.Allowed() {
    // deny access; result.Reason explains why
}
```

无法直接读取内核 inode generation 的集成方可以自行填充 `netsec.FileState`。
`Path` 只用于诊断，不参与认证；因此硬链接到同一 inode 不会改变结论。

## 认证输入

- 本机 HMAC（类型字节 `2`）按固定顺序覆盖：`security.ima`、
  `security.selinux`、inode、`i_generation`、uid、gid、mode。
- 可移植签名（类型字节 `5`）覆盖相同字段，但刻意排除 inode 与
  `i_generation`，所以复制到另一 inode 后仍可验证。
- 字节值使用 8 字节长度前缀，标量使用大端编码，避免拼接歧义。
- HMAC 默认使用 HMAC-SHA-256；对 20 字节摘要也支持 HMAC-SHA-1。
  这里保证的是摘要长度与算法兼容，规范化输入格式由本包定义。
- 签名支持 Ed25519（直接签消息）以及 RSA/ECDSA（签 SHA-256 摘要）。
  受信公钥可通过 `ParsePublicKey` 从 PEM、证书或原始 Ed25519 公钥加载。

`Evaluate` 回答“当前快照是否可信”。如果引擎保留了先前可信快照，
`EvaluateTransition` 会先重新执行密码学校验，再区分 uid/mode 等属性改动
（`ReasonAttributeMismatch`）、inode/generation 改变
（`ReasonInodeMismatch`）以及两者同时改变。单次 HMAC 失败本身无法证明是
哪一项输入变化，因此只有提供基线时才做这种归因。

## Linux 路径适配

`ReadState` 在同一文件描述符上读取 xattr、`fstat` 和 `FS_IOC_GETVERSION`
得到的 `i_generation`。文件系统或内核不支持 generation ioctl 时返回
`ErrGenerationUnavail` 而不是用零值降级。非 Linux 平台返回
`ErrUnsupported`；核心 `FileState` 评估在所有平台可用。

## 测试

`go test ./...` 覆盖 uid 篡改、把合法 EVM 复制到另一 inode、真实硬链接、
缺少主机密钥以及 Ed25519/RSA/ECDSA 可移植签名验证。
