# materialsim

物料流模拟器：每个物料实体携带**来源份额向量**（`map[sourceID]*big.Rat`，精确有理数），数量为整数。

## 事件模型（`sim.Event`）

- `receive`：从某来源引入整数数量物料，来源向量为 `{source: qty}`。
- `split`：按整数份数拆分，各份来源份额按比例精确分配；份数之和必须等于输入数量（守恒），否则拒绝且不改动状态。
- `merge`：合并多个实体，来源向量逐来源相加（守恒）。
- `loss`：整数损耗，所有来源份额按 `剩余/原始` 比例有理扣减，并显式记入损耗台账（`Simulator.Losses`）。

每个事件要么严格守恒，要么显式记损耗；其余情况一律拒绝。输出实体 ID 由事件 ID 确定性派生（`<eventID>/<i>`）。

## 追溯 / 重放 / 幂等

- `Trace(id)`：把任意终品表示为来源的精确线性组合，如 `cast/0 (qty 9) = 45/8*oreA + 27/8*oreB`；`VerifyTrace(id)` 核对系数和等于数量。
- `Replay(log)` / `VerifyReplay()`：从空状态重放事件日志，结果必须与当前状态逐实体一致。
- 重复事件幂等：事件 ID 去重，重复投递是 no-op（`Apply` 返回 `duplicate=true`），含重复事件的日志重放结果不变。

## 运行

```sh
GO111MODULE=on go test ./...
GO111MODULE=on go run ./cmd/demo
```
