# eventgraph

Go 分析库：从带**本地序号**和**收发关联**的事件构建因果偏序图（happens-before）。

## 设计原则

- **墙钟仅作展示**：`Event.WallClock` 从不参与排序或时钟计算，因果只来自
  进程内本地序号（`Seq`）与消息配对（`Message` 相同的 send → recv）。
- **乱序导入安全**：边在两端事件到齐时才连接，`AddEvent` 任意顺序调用，
  最终图结构一致；`Validate` 用消息图不变量核对导入是否完整一致。
- **重复事件**：因果内容一致的重复添加被合并（保留最早展示墙钟）；
  内容矛盾的重复或关联记录为 `Contradiction`，成环时附环路径证据。
- **确定性输出**：`TopoSort` 用 Kahn 算法 + 最小 ID 堆打破平局，
  结果只依赖图结构；`LamportClocks` 沿拓扑序计算（最长因果链 + 1）。

## API 一览

| 入口 | 作用 |
| --- | --- |
| `NewGraph` / `AddEvent` | 增量建图，自动去重合并、记录矛盾 |
| `TopoSort` | 确定拓扑序；成环返回 `*CycleError`（含环证据） |
| `LamportClocks` | 每事件 Lamport 逻辑时钟 |
| `HappensBefore` | 因果可达性查询 |
| `Validate` | 不变量核对：recv 无 send、重复 send/recv、序号空洞、在途消息、环 |
| `Contradictions` / `Describe` | 矛盾清单 / 带时钟与墙钟的可读输出 |

## 不变量（`Validate`）

1. 每个 recv 的 message 恰好有一个 send（`recv-without-send`、`duplicate-send`、
   `duplicate-recv`）；
2. send 无 recv 仅作信息性报告（`unmatched-send`，消息可能在途）；
3. 每进程 `Seq` 从 1 连续（`seq-gap` 提示导入缺失）；
4. happens-before 无环（`cycle`，附证据路径）。

## 运行测试

```sh
go test ./...
```
