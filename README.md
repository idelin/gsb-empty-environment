# 双时态合同履约状态机

仅用 Go 标准库实现。两条时间轴：

- **业务时间（valid time）**：事实在业务上发生的时间，合同在其上经历 `草案 → 生效 → 履行中 → 终止`。
- **知悉时间（transaction time）**：事实被系统录入/知悉的时间，支持补交与作废历史事实而不改写历史。

## 模型

- `Terms`：条款（生效点、履行起点、终止点、合同总量），按知悉时间版本化（`AmendTerms` 时态改约）。
- `Voucher`：交付凭证，携带业务时间与事务时间区间 `[TxFrom, TxTo)`；作废即关闭区间。
- `StateAt(business, knowledge)` / `CompletionAt(business, knowledge)`：任意双时态切片下的状态与完成度百分比。

## 运行

```sh
go test ./... -v   # 测试
go run .           # 验收场景演示
```
