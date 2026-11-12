# WebAuthn凭据注册冲突

认证服务注册新公钥时，不能把相同凭据ID绑定到另一账号或忽略设备证明的用途。写供Go注册宿主调用的事务包，验证回调返回结构化来源、挑战、RP和公钥，全部匹配后原子占用凭据ID；重复同请求可返回原结果，异账号或异钥冲突拒绝。用并发注册与响应丢失测试唯一归属。

## 包结构

- `register.go`：`Service.Register` 编排。宿主实现 `Verifier`，只负责设备证明（attestation）密码学校验并返回结构化的 `Verified`（用途、来源 origin、挑战、RP ID、凭据 ID、公钥）；包负责策略匹配与事务。
- `memory.go`：`MemoryStore`/`memoryTx` 参考事务实现，提交点对凭据 ID 做原子裁决。
- `keyed_mutex.go`：按凭据 ID 串行化内存注册，等价于 SQL 宿主对 `credential_id` 的 `SELECT ... FOR UPDATE` 行锁。

## 安全规则

- 仅接受 `PurposeAttestation`；断言（assertion）响应打到注册端点返回 `ErrWrongCeremony`。
- origin、RP ID、挑战逐项与期望参数匹配（挑战用恒定时间比较），任一不符即拒绝且不写任何状态。
- 一次性挑战与凭据占用在同一事务内完成，重放响应无法注册第二次。
- 凭据 ID 已存在时：同账号同公钥 → 返回原结果（`Replayed=true`）；异账号 → `ErrCredentialAccountConflict`；同账号异公钥 → `ErrCredentialKeyConflict`。
- 幂等键按“请求 + 验证结论”指纹绑定；同键不同载荷返回 `ErrIdempotencyConflict`。

## SQL 宿主接入

实现 `Store`/`Tx` 即可。建议表约束：

```sql
CREATE TABLE credentials (
  credential_id BYTEA PRIMARY KEY,
  account_id    TEXT  NOT NULL,
  public_key    BYTEA NOT NULL
);
CREATE TABLE registration_challenges (
  challenge BYTEA PRIMARY KEY
);
CREATE TABLE registration_idempotency (
  idempotency_key BYTEA PRIMARY KEY,
  fingerprint     BYTEA NOT NULL,
  credential_id   BYTEA NOT NULL,
  account_id      TEXT  NOT NULL
);
```

在可串行化事务中先 `SELECT ... FOR UPDATE` 查 `credentials`，插入凭据、删除挑战、写幂等行，唯一约束冲突时让驱动错误映射为对应哨兵错误并重开事务重查。

## 测试

`go test -race ./...` 覆盖：用途/来源/RP/挑战不匹配、异账号与异钥冲突、幂等重放、响应丢失的自然重放、挑战单用，以及 32 路并发抢占同一凭据 ID 与同账号并发重试的唯一归属。
