# 双时态合同履约状态机

Go 标准库实现，无第三方依赖。

## 模型

- **业务时间**（valid time）：合同条款与交付事实生效的时间轴，合同业务期为 `[ValidFrom, ValidTo)`。
- **知悉时间**（transaction time）：系统获知事实的时间轴，由 `AdvanceKnowledge` 单调推进。
- 交付凭证为追加式记录，知悉区间 `[KnownFrom, KnownTo)`；作废仅关闭区间，历史切片可完整重建。

## 状态机（沿业务时间）

`草案`（t < ValidFrom）→ `生效`（有效期内且无已知交付）→ `履行中`（已有已知交付）→ `终止`（t ≥ ValidTo）

## API

- `NewContract(validFrom, validTo, requiredTotal)` 创建合同
- `AdvanceKnowledge(t)` 推进知悉时钟（单调）
- `RecordDelivery(businessTime, quantity)` 在当前知悉时刻录入/补交交付
- `VoidDelivery(id)` 在当前知悉时刻作废凭证
- `StateAt(knowledge, business)` / `CompletionAt(knowledge, business)` / `SnapshotAt(knowledge, business)` 查询任意双时态切片

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 验收场景演示
```
