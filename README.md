# logreasm — 多来源日志重组工具

按字节片段接收多来源日志流，重组为记录。每个片段携带 `source`、`epoch`、
`seq`（片段序号）与 `data`。

## 规则

- 记录以**时间戳行**开始（ISO8601 前缀），**缩进行**（空格/制表符开头）为续行。
- 片段缺失时，缺口之后的续行**不会**被接到旧记录上，而是形成带缺口证据的
  orphan 记录。
- 缺片补到后，已输出的记录被**原地修订**：ID 稳定不变，`Version` 递增，
  被合并的 orphan 记录以 `retract` 事件撤销。
- 记录 ID 由首行的不可变位置（`source:epoch:片段序号@片内偏移`）派生，
  补片不会改变它。
- 行号为来源/epoch 流内的 1-based 行号（按已接收字节计算），补片后随之修订。
- 缺口证据：`gap_before {missing_after, missing_before}`（记录前有空洞）与
  `gap_after`（记录后有空洞）。
- epoch 增大视为来源重启（新流，行号归零）；epoch 回退的片段作为过期数据丢弃。
- 重复片段（同 seq 同内容）忽略；冲突片段（同 seq 不同内容）保留先到者并告警。
- 超过 `MaxLine`（默认 1 MiB）的行被截断并置 `Truncated`。
- `Records()` 输出按 `(时间戳, source, epoch, 行号)` 排序，同 tick 跨来源
  排序确定。

## 使用

库：

```go
eng := logreasm.NewEngine(logreasm.WithMaxLine(1 << 20))
events := eng.Ingest(logreasm.Fragment{Source: "web-1", Epoch: 1, Seq: 0, Data: b})
records := eng.Records()
```

CLI（stdin 每行一个 JSON 片段，stdout 输出事件流，EOF 后输出最终记录集）：

```sh
go run ./cmd/logreasm <<'NDJSON'
{"source":"web-1","epoch":1,"seq":0,"data":"2026-01-01T00:00:00Z boot\n  loading\n"}
{"source":"web-1","epoch":1,"seq":2,"data":"  stray\n"}
{"source":"web-1","epoch":1,"seq":1,"data":"  backfilled\n"}
NDJSON
```

## 测试

```sh
go test ./...
```

覆盖：基本记录与行号、缺片不粘连、补片修订与 ID 稳定、重启与过期 epoch、
重复/冲突片段、超长行截断、同 tick 跨来源排序、乱序与跨片断行、重解析幂等。
