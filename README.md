# logrec — 多源日志记录重组器

纯 Go 标准库实现。把多源、可分块、可乱序到达的日志行重组为记录（record），
并输出带稳定 ID 与修订版本的记录流。无外部监控平台依赖，遥测事件在本地合成，
可由夹具（fixture）回放。

## 核心语义

- **起始行 / 续行**：匹配 `Rules.StartPattern` 的行开启新记录并关闭上一条；
  缩进行（前导空格/制表符，可自定义）拼入当前记录。非起始、非缩进行自成一条记录。
- **行身份**：每行保留 `(source, epoch, seq)`；`seq` 即源流中的原始行号。
  记录 ID 为 `source/epoch/起始seq`，跨修订与 checkpoint 稳定。
- **流切分**：`FeedChunk` 接受任意字节切块，跨块半行缓存在 `lineBuf`，
  异常堆栈跨块仍是一条记录。
- **多源 / epoch**：每个 `(source, epoch)` 独立组装状态，互不污染。
- **序号缺口**：`seq` 跳跃产生 `gap_detected` 遥测；缺行迟到时按 seq 归位。
- **迟到更正**：同 seq 不同文本 → 所属记录修订（`Revision+1`）并重发；
  同 seq 同文本 → `duplicate_line`，忽略。
- **迟到续行**：按 seq 找到属主记录插入；若该记录已关闭，则修订原记录重发，
  **绝不**拼进另一条正在打开的记录。
- **规则热换**：`SetRules` 只影响之后分类的行，已消费行的身份与归属不变。
- **超长记录**：超过 `MaxRecordLines/MaxRecordBytes` 的记录置 `Truncated`，
  超出的行计入 `Dropped`（保留 seq 归属），遥测 `record_truncated`，不静默丢行。
- **不完整尾部**：`Close` 时未换行的半行补成最后一行；未关闭的记录以
  `CloseEOF` 发出（`Complete()==false`）。`FlushPending` 以 `CloseFlush` 强制关闭。
- **背压**：全局缓冲预算（半行 + 待定记录）耗尽时，feed 返回
  `ErrBackpressure` 且不占位；`FeedChunk` 报告已消费字节数，调用方
  `FlushPending` 释放后重试。
- **Checkpoint**：`Snapshot`/`Restore` 序列化全部状态（半行、待定记录、
  已关闭记录历史、seq 索引、预算用量、遥测日志），恢复后可无缝续传。
- **遥测**：`Telemetry` 本地事件流，可 `WriteJSONL` 导出；`fixture.go`
  提供 `Op` 脚本与 `Replay`，`testdata/fixture_ops.jsonl` 为可回放样例。

## 运行

```sh
go test ./...          # 全部测试
go run ./cmd/logrec-demo   # 演示：37 字节切块 + 中途 checkpoint/restore + 迟到更正
```

## 布局

- `types.go` — Line / Record / Rules / Emission
- `assembler.go` — 单 (source, epoch) 组装状态机
- `multiplexer.go` — 多源路由、预算与背压
- `snapshot.go` — checkpoint 存取
- `telemetry.go` — 本地遥测事件
- `fixture.go` — 可回放夹具
- `cmd/logrec-demo` — 运行样例
