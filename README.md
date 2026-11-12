# PCR策略授权代数

系统升级后 PCR 值变化，设备用授权密钥对新的 PCR 策略签名来放行密封密钥，攻击者想重放旧版本含漏洞系统的已签策略，或拿别的对象的策略签名。为Go启动服务写供解封前调用的策略授权校验包：签名覆盖 approvedPolicy‖policyRef，公钥名称必须等于对象策略里固定的授权密钥名称，policyRef 绑定设备与对象，另有单调代数，低于 NV 计数器的签名一律拒绝，放行成功后才推进计数。用换 policyRef、旧代数、换密钥、放行失败时不推进计数的样例验证。

## 实现：`policyauth` 子包

对应 TPM2_PolicyAuthorize 语义，解封前调用 `Verifier.Authorize`，校验顺序：

1. **密钥名称钉扎**：对出示的 ECDSA 公钥派生 TPM 风格名称（SHA-256 名称算法标识 + PKIX DER 摘要），必须与对象策略固定的 `AuthorizedKeyName` 常量时间相等，否则 `ErrUnauthorizedKey`。
2. **policyRef 绑定**：token 的 `PolicyRef` 必须与对象策略中的 `{DeviceID, ObjectID}` 完全一致，否则 `ErrPolicyRefMismatch`（长度前缀的确定性二进制编码参与签名，杜绝拼接歧义）。
3. **approvedPolicy 匹配**：签名中的 PCR 策略摘要必须等于对象策略当前批准的摘要；升级后旧镜像摘要在此被拒，返回 `ErrPolicyMismatch`。
4. **签名验证**：ECDSA P-256 验证签名消息 `approvedPolicy ‖ policyRef ‖ generation`。代数一并纳入签名，防止给旧签名贴新高代数；失败返回 `ErrBadSignature`。
5. **单调代数**：`Generation < NV 计数器当前值` 一律 `ErrStaleGeneration`。

计数器（`CounterStore`，生产中对应 TPM NV 索引）仅在解封回调成功后 `Advance`，推进到 `max(当前, 代数)+1`；校验失败或解封失败均不推进，同一代数可重试。提供内存实现 `MemoryCounter` 与原子写文件实现 `FileCounter`（同目录临时文件 + rename + fsync，崩溃不回退）。

## 验证

`go test ./...`（含 `-race`）覆盖：

- 换 policyRef（换对象 / 换设备）→ `ErrPolicyRefMismatch`，计数不动
- 旧代数（含升级后重放旧 token）→ `ErrStaleGeneration`，计数不动；新代数通过并抬高下限
- 换密钥（攻击者自签 / 冒用公钥）→ `ErrUnauthorizedKey`
- 旧 PCR 策略 / 别的对象策略摘要 → `ErrPolicyMismatch`
- 放行（unseal）失败 → 包装原始错误，NV 计数不推进，同代数可重试
- 篡改代数或签名 → `ErrBadSignature`
- `FileCounter` 跨实例持久化与单调性
