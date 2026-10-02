# 声明式数据脱敏与合规导出器

数据安全导出转换内核：根据**访问者角色**与**合规级别**，以声明式策略对
手机号、身份证、薪资等敏感字段执行掩码（Masking）、差分加噪（Laplace Noise）
或哈希假名化（Hash Pseudonymization）。纯 Go 标准库实现。

## 设计

- `compliance/transform.go` — 转换原语：`Pass` / `Deny` / `Mask` / `HashPseudonym` / `Bucket` / `LaplaceNoise`
- `compliance/policy.go` — 声明式策略：角色 → 字段 → 转换；**未声明字段默认拒绝**（最小暴露）
- `compliance/exporter.go` — 导出器：每次导出使用独立密钥，JSON / CSV 输出

### 关联一致性

假名化使用 `HMAC-SHA256(导出密钥, 明文)`：同一次导出内相同明文映射到相同假名
（可关联统计），不同导出使用不同会话密钥，假名不可跨导出关联。差分加噪的
随机源同样由导出密钥派生，同密钥导出可复现（便于审计回放）。

### 内置角色策略

| 字段 | hr_admin (L1) | finance (L2) | contractor (L3) |
|---|---|---|---|
| 姓名 | 明文 | 明文 | 哈希假名 `EMP-xxxx` |
| 手机号 | 明文 | 掩码 `138****5678` | 拒绝 `[REDACTED]` |
| 身份证 | 明文 | 掩码 前6后4 | 拒绝 `[REDACTED]` |
| 薪资 | 明文 | 明文（薪资明细） | 区间 `20000-25000` |

## 运行样例

```sh
go run ./cmd/demo
```

## 测试

```sh
go test ./...
```
