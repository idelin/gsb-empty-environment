# 多版本医疗处方配伍禁忌检查器

双时态（bitemporal）判定器，仅使用 Go 标准库。

## 模型

- **业务时间（valid time）**：处方开立时间 `Prescription.IssuedAt`、规则临床生效时间 `Rule.ValidFrom`。
- **知悉时间（transaction time）**：规则录入规则库的时间 `Rule.RecordedAt`。
- 规则库 `RuleStore` 只追加、不修改，支持按任意知悉时点重放快照（`Snapshot`）。

## 两类判定

- `CheckCompliance(store, rx)`：业务时间与知悉时间均锚定在处方开立时刻，
  用"当时的知识"判定历史处方开立时是否合规（`禁用`命中判不合规，`慎用`仅记录）。
- `AuditRetrospective(store, rx, now)`：用当前最新规则库重评历史处方，
  仅报告开方之后才知悉的规则命中，即追溯风险。

## 运行

```sh
# Go 1.11 需显式开启 module 模式；缓存目录需可写
export GO111MODULE=on GOCACHE=/private/tmp/go-cache
go test ./... -v   # 测试
go run ./cmd/demo  # 验收边界场景演示
```

## 验收边界

处方 P 于业务时间 10 开立（含甲药、乙药）；知悉 20 指南 2024-v2 将甲乙列为慎用；
知悉 25 查询：开方合规性 = 合格，风险审计 = 标记追溯风险。
对应测试：`TestAcceptance_CompliantAtPrescribingTime`、`TestAcceptance_RetrospectiveRiskFlagged`。
