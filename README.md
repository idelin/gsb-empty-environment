# bitemporal

双时态（Valid Time + Transaction Time）价格规则引擎，仅使用 Go 标准库。

## 语义

- 每条价格规则记录业务生效区间 `[ValidFrom, ValidTo)`（左闭右开）与系统知悉时间 `KnownAt`。
- 查询 `PriceAt(validTime, asOf)` 只使用 `KnownAt <= asOf` 且未在 `asOf` 前被撤销的规则。
- 区间重叠时按知悉时间后入为主（`KnownAt` 最大者胜出，相同则后添加者胜出）。
- `Retract(id, at)` 只影响 `asOf >= at` 的查询，更早的知悉视图保持不变。
- 金额一律为整数分（`int64`）。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 验收场景样例
```
