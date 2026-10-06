# eventrules

同对象多事件的有效值裁决引擎（Go 1.11+，零依赖）。

## 裁决规则

- 同一 `Object` 下多个 `Event` 竞争有效值：`Priority`（业务优先级）高者胜；
  并列时 `Seq`（事件序号）大者胜；再并列时 `ID` 字典序小者胜（保证确定性）。
- `Retract(targetID)` 只撤回指定事件 ID，不影响同对象的其它候选。
- 撤回允许先于事件到达：先立墓碑，事件后到时直接视为已撤回；重复撤回幂等。
- 当前赢家被撤回后，次优候选自动显露为新的有效值。

## 差量输出

`Apply` / `Retract` 均返回 `Delta`，仅当有效值变化时 `Changed=true`，
携带新旧有效事件，可直接对接下游输出流。

## 重复与冲突载荷

- 同 ID 且内容完全一致：幂等忽略，计入 `Engine.Duplicates`。
- 同 ID 但内容不一致：保留先到者、忽略后来者，计入 `Engine.Conflicts`。

## 全候选排序模型核对

`Engine.Verify()` 从全部事件出发重新聚合、过滤墓碑并全量排序，
逐位比对增量维护的候选队列与有效值，返回不一致项列表（空表示通过）。
随机操作序列的交叉核对见 `rules/engine_test.go` 的 `TestFullModelCrossCheck`。

## 运行

```sh
go test ./...
go run ./cmd/demo
```
