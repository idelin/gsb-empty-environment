# tracevault

可追溯轨迹时态加密索引与假名化审计器 —— 保护隐私的时空数据检索组件。
纯 Go 标准库实现，无第三方依赖。

## 设计

| 关注点 | 机制 |
| --- | --- |
| 实体假名化 | 带密钥时变 PRF：`HMAC-SHA256(masterKey, "pseudonym" ‖ entityID ‖ epoch)`，假名每个 epoch（默认 1 小时）轮换，跨时段不可链接 |
| 空间索引 | 坐标量化到网格（默认 0.01° ≈ 1km），单元 ID 经加盐哈希 `HMAC-SHA256(salt, "cell" ‖ cellID)` 后作为索引键 |
| 记录加密 | 每条记录用派生密钥 `HMAC(masterKey, "record" ‖ entityID ‖ epoch)` 做 AES-256-GCM 加密，假名作 AAD 绑定 |
| 日常只读查询 | 仅返回脱敏聚合热力分布：`cellHash → count`，不含任何实体信息与明文坐标 |
| 定向还原 | 司法审计凭证（HMAC 签名，绑定单一实体 + epoch 区间 + 有效期 + nonce）验签通过后，逐 epoch 重算假名、定向解密该实体轨迹 |
| 审计日志 | 仅追加、哈希链（每条含前一条哈希），成功与拒绝的还原尝试均留痕，`Verify()` 可检测篡改 |

## 文件

- `crypto.go` — 密钥派生（HMAC-SHA256）与 AES-256-GCM 加解密
- `pseudonym.go` — 时变假名 PRF
- `grid.go` — 网格量化与加盐单元哈希
- `store.go` — 加密记录存储与假名/单元索引（并发安全）
- `credential.go` — 审计凭证签发与验证
- `auditlog.go` — 哈希链审计日志
- `vault.go` — `Vault` 门面：`Ingest` / `Heatmap` / `Reveal`
- `cmd/demo/main.go` — 端到端运行样例
- `vault_test.go` — `go test` 测试套件

## 运行

```sh
go test -race -v ./...   # 测试
go run ./cmd/demo        # 样例：批量写入 → 热力分布 → 凭证定向还原 → 伪造拒绝 → 审计链
```

## 验收边界对照

- **海量位置存储**：`Store` 为并发安全的内存索引（假名索引 + 单元索引），`TestConcurrentIngest` 验证并发写入，`BenchmarkIngest` 可测吞吐；接口与持久化后端解耦，可平滑替换。
- **日常只读查询只能获取脱敏聚合热力分布**：`Heatmap` 只返回加盐哈希到计数的映射；`TestIngestStoresNoPlaintext` 断言落盘记录不含明文实体 ID 与坐标。
- **授权后精准定向还原**：`Reveal` 验签后仅还原凭证绑定的实体与时间窗（`TestRevealValidCredential`、`TestCredentialBoundToEntity`）；伪造签名、过期凭证均被拒绝并留痕（`TestRevealForgedSignature`、`TestRevealExpiredCredential`）。
- **完整审计日志**：哈希链审计，篡改可检测（`TestAuditLogTamperEvidence`）。

## 测试结果

```
=== RUN   TestPseudonymTimeVarying      --- PASS
=== RUN   TestIngestStoresNoPlaintext   --- PASS
=== RUN   TestHeatmapAggregation        --- PASS
=== RUN   TestRevealValidCredential     --- PASS
=== RUN   TestRevealForgedSignature     --- PASS
=== RUN   TestRevealExpiredCredential   --- PASS
=== RUN   TestCredentialBoundToEntity   --- PASS
=== RUN   TestAuditLogTamperEvidence    --- PASS
=== RUN   TestConcurrentIngest          --- PASS
=== RUN   TestIngestRejectsBadInput     --- PASS
PASS
ok  	tracevault	1.787s   (go test -race)
```
