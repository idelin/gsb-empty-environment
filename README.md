# tracevault — 可追溯轨迹时态加密索引与假名化审计器

保护隐私的时空数据检索组件，仅使用 Go 标准库实现。

## 设计

| 需求 | 机制 |
| --- | --- |
| 实体 ID 假名化 | 带密钥时变 PRF：`HMAC-SHA256(K_pseudo, entityID ‖ bucket)`，假名随时间桶轮换 |
| 空间坐标索引 | 加盐网格哈希：`HMAC-SHA256(K_grid, cellX ‖ cellY ‖ bucket)`，截断 128 bit |
| 轨迹机密性 | 按“实体 × 时间桶”派生 AES-256-GCM 密钥加密，AAD 绑定假名防移植 |
| 日常只读查询 | 仅返回聚合热力分布（网格令牌 → 计数），k-匿名抑制稀疏网格 |
| 司法定向还原 | 监管方持审计密钥签发 `Credential`（目标实体 + 时间范围 + 过期时间 + HMAC 签名），系统验签后仅解密授权范围内的轨迹 |
| 审计留痕 | 仅追加日志 + SHA-256 哈希链，成功与拒绝均记录，`Verify()` 可检出篡改 |

主密钥经 HMAC 域分离派生四个子密钥（假名 / 网格盐 / 记录加密 / 审计），
即使单个子密钥泄露也不波及其他安全域。

## 运行样例

```sh
go run ./cmd/demo
```

输出（节选）：

```
[写入] 1000 名用户 x 12 个轨迹点 = 12000 条加密记录
[热力] 时间桶 497475：64 个匿名网格，共 4000 次观测（无任何实体信息）
[授权] 签发凭证：目标=user-0042 时间桶=[497474,497478]
[还原] 解密出 user-0042 的 12 个轨迹点
[审计] 日志 1 条，哈希链校验：通过
```

## 测试

```sh
go test ./...        # 单元测试
go test -race ./...  # 并发竞态检测
```

覆盖：假名时变性、网格盐、聚合与 k-匿名、合法凭证还原、授权范围限制、
伪造/过期/错钥凭证拒绝、审计链篡改检出、并发写入。

## API 速览

```go
keys  := tracevault.DeriveKeys(master)              // 32 字节主密钥
vault := tracevault.NewVault(keys, tracevault.DefaultBucketSize, 0.01)

vault.Insert("user-0042", 31.2304, 121.4737, ts)    // 写入（自动假名化+加密）
heat := vault.Heatmap(bucket, 5)                    // 只读聚合，k=5 匿名

cred, _ := tracevault.IssueCredential(keys.AuditKey(),
    "沪检刑调[2026]第001号", "user-0042", from, to, expiresAt)
pts, _ := vault.Reveal(cred)                        // 定向还原 + 审计留痕
vault.AuditLog().Verify()                           // 校验审计链完整性
```
