# logreasm — 多来源日志重组工具

按字节片段接收多来源数据，每个片段带 `source`、`epoch`、`seq`（片段序号）。
记录以时间戳行开始，缩进行为续行。

## 语义

- **缺片不拼接**：片段缺口之后的续行不会接到缺口前的记录上，而是形成
  孤儿记录，并携带缺口证据 `gapBefore:{missingFrom,missingTo}`；缺口前
  的记录标记 `cutByGap`。
- **补片修订**：迟到的片段填补缺口后，已输出的记录被修订（`rev` 递增），
  被合并的孤儿记录以 `retract` 事件撤回；记录 ID 全程稳定（以首行在流中
  的锚点 `(seq, offset)` 标识）。
- **行号**：`startLine`/`endLine` 为流内原始行号；记录前存在缺口时
  `lineBaseKnown=false`，行号相对于所在连续段。
- **重启**：同一 source 的新 epoch 构成独立流，各自从行号 0 开始。
- **重复片**：载荷相同的重复片段忽略并计入 `dups`；载荷冲突的片段保留
  先到者并记入 `conflicts`。
- **超长行**：超过 `-max-line`（默认 1 MiB）的行被截断并标记 `truncated`。
- **排序**：最终输出按 `(tick, source, epoch, startLine)` 排序，同一
  tick 跨来源按 source 字典序，确定性可重复。

## 用法

```sh
go test ./...
go build ./cmd/logreasm
printf '{"source":"app1","epoch":1,"seq":0,"data":"2026-01-01T00:00:00Z boot\\n"}\n' | ./logreasm
```

输入为 JSONL 片段，每个到达的片段触发 `upsert`/`retract` 事件流，
EOF 时输出一行 `{"kind":"final", ...}` 全量有序结果。

## 库接口

```go
r := logreasm.NewReassembler(logreasm.WithMaxLineLen(1<<20))
events := r.Ingest(logreasm.Fragment{Source: "app1", Epoch: 1, Seq: 0, Data: data})
records := r.Records() // 有序快照
```
