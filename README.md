# EVM属性HMAC与inode绑定评估

EVM 用 security.evm 扩展属性保护文件的安全相关属性（security.ima、security.selinux、uid、gid、mode），攻击者离线改属性，或把另一个文件的合法 HMAC 拷过来。为 Go 完整性引擎写供文件访问前调用的评估包：本机 HMAC 形式（类型字节 2）按固定顺序对各属性值、inode 号、i_generation、uid、gid、mode 做 HMAC，可移植签名形式（类型字节 5）不计入 inode 与 generation；HMAC 形式用主机密钥重算比对，签名形式用受信公钥验证；属性被改、文件被复制到另一 inode、硬链接到另一路径但 inode 相同分别给出不同结论；密钥未加载时一律拒绝，不退回只检查 IMA；类型字节未知拒绝。用改 uid、复制 HMAC 到另一文件、硬链接、缺密钥和可移植签名验证。

## 设计

包名 `netsec`，只对调用方采集的 `FileMeta` 做纯元数据评估，不直接读
`security.*` 扩展属性，因此可在非 Linux 平台做确定性测试。

- `Engine.Evaluate(FileMeta) Result`：文件访问前的唯一入口，fail-closed。
- `canonicalInput` 固定编码顺序：类型 → security.ima → security.selinux
  → inode → i_generation → uid → gid → mode；变长属性带 4 字节小端长度前缀。
  类型 5 跳过 inode/generation，其余顺序不变。
- `NewHMACAttr` / `NewPortableSignatureAttr`：构造 `security.evm` 载荷
  （`1 字节类型 + 32 字节 HMAC-SHA256` 或 `1 字节类型 + 签名`）。
- 可移植签名默认适配 `crypto/ed25519`，算法通过 `PublicKey`/`Signer`
  接口解耦，可替换为 RSA 等实现。

## 结论矩阵（类型字节 2，本机 HMAC）

| 场景 | 结论 Reason | 说明 |
| --- | --- | --- |
| 属性未变、inode 未变 | `HMACValid`（放行） | 重算 HMAC 完全一致 |
| 离线改 uid/gid/mode/ima/selinux | `AttrMismatch`（拒绝） | 全量与签发期 inode 重算均不匹配 |
| 文件连合法 HMAC 复制到新 inode | `InodeMismatch`（拒绝） | 当前属性 + 签发期 inode 重算命中 |
| 硬链接到新路径、inode 相同 | `HMACValid`（放行） | 规范输入不含路径 |
| 主机密钥未加载 | `NoKey`（拒绝） | 不退回只检查 IMA |
| 未知类型字节 / 畸形 / 缺 xattr | `UnknownType` / `Malformed` / `NoXattr` | 一律拒绝 |

类型字节 5（可移植签名）不计 inode/generation：文件迁移到新 inode 仍
`SignatureValid` 放行；属性被改或非受信密钥签发则 `BadSignature`；受信
公钥未加载则 `NoKey`。

`FileMeta.EVMBoundIno/EVMBoundGeneration` 是签发期 inode 绑定记录（由
完整性引擎随扩展属性存放在安全清单中）。缺记录时无法区分“属性篡改”与
“跨 inode 复制”，保守按 `AttrMismatch` 拒绝，绝不放行。

## 测试

`go test ./...` 覆盖：改 uid（及 gid/mode/ima/selinux/generation）、
把合法 HMAC 复制到另一 inode、同 inode 硬链接（含真实文件系统用例）、
密钥未加载不退回 IMA、可移植签名验证与跨 inode 迁移、不受信签名、
未知/遗留类型字节、畸形与缺失载荷、固定编码顺序。
