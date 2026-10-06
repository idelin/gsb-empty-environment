# merge — 多源记录字段级裁决合并库

每个字段候选（`Candidate`）携带：

- `Priority`：来源优先级，越大越权威
- `From`/`To`：有效区间 `[From, To)`，零值表示无界
- `Seq`：入库序号，同优先级时大者胜出
- `Value`：三态取值（`Missing` / `Null` / `Val`）

## 裁决规则

1. 按查询时间过滤有效区间外的候选（`OutOfWindow`）。
2. 缺失候选不参与裁决（`IgnoredMissing`），因此**缺失不会覆盖显式 null**。
3. 逐字段裁决：优先级高者胜；同级序号大者胜。
4. **同优先级同序号而异值 → 报冲突**（`Conflict=true`），并按
   （来源名升序、取值规范串升序）确定性选出代表值；同值并列不算冲突。
5. 每个候选的处置结果（胜出/并列/冲突/被压制/窗口外/缺失）都保留在
   `FieldResult.Candidates` 中，顺序与输入一致，构成完整解释。

## 实现与核对

- `Adjudicate`：单遍扫描维护最优并列组，不做全量排序。
- `NaiveAdjudicate`：直接“过滤 + 全量排序”的参照模型。
- `oracle_test.go` 用 2 万组随机候选集核对两者结果逐位一致。

```sh
GO111MODULE=on go test ./...
```
