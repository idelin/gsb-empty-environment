# logalloc — 批次序号日志分配器

先预留批次序号再写数据；失败可留下空洞，但发布流必须用显式跳过记录
封闭空洞才能推进；恢复区分「已预留未决定」与「已跳过」；持久化切点
保证连续消费与不重复分配。

## 模型

- **WAL**（`logalloc.wal`，逐行 JSON、写后 fsync）记录四类事件：
  `reserve` / `commit` / `skip` / `checkpoint`。
- **状态机**：每个序号 `reserved → committed | skipped`，决定后不可更改。
- **分配器** `Allocator`：`Reserve()` 先落盘再发号；`Commit(seq, data)`
  补写数据；`Skip(seq)` 写显式跳过记录封闭空洞。
- **发布流** `Publisher`：严格按切点 +1 消费，`Next()` 推进即持久化新
  切点；遇到未决定的空洞返回 `ErrHoleNotClosed`，绝不跨越。
- **恢复** `Open(dir)`：重放 WAL 并返回 `Report`，明确区分
  `Undecided`（已预留未决定，需上层裁决）与 `Skipped`（终态）；
  `NextSeq` 恢复为已用最大序号 +1，保证不重复分配。
- **核对** `Verify()`：切点及之前每个序号都已决定（连续消费），
  分配起点大于一切已出现序号（不重复分配）。重放阶段另强制：
  序号不重复分配、决定不重复、切点逐序 +1 推进且不越过空洞，
  违反即拒绝打开。

## 使用

```go
l, rep, _ := logalloc.Open(dir)   // 打开并恢复
a, p := l.Allocator(), l.Publisher()

seq, _ := a.Reserve()             // 1. 先预留（落盘）
a.Commit(seq, data)               // 2a. 写数据成功
a.Skip(seq)                       // 2b. 或失败放弃：显式跳过封闭空洞

out, err := p.Next()              // 3. 发布流按序推进
// err == logalloc.ErrHoleNotClosed -> 有空洞，需 Commit 或 Skip 裁决
```

恢复后处理遗留空洞：

```go
for _, seq := range rep.Undecided {
    a.Skip(seq) // 或重试成功后 a.Commit(seq, data)
}
```

## 测试与演示

```sh
GO111MODULE=on go test ./...
GO111MODULE=on go run ./cmd/logalloc-demo /tmp/logalloc-demo
```
