# rga — Replicated Growable Array (Go)

基于因果树的协同富文本底层数组（CRDT），仅使用 Go 标准库。

## 设计

- 每个字符是链表节点，记录全局唯一 `ID{Site, Seq}` 与因果前驱（父节点）`Parent`。
- `Seq` 是 Lamport 时钟，保证子节点 ID 恒大于其父节点 ID。
- 并发在同一前驱后插入时，兄弟节点按 ID **降序** 排列（ID 大者在前），
  因此所有副本无论以何种顺序收到操作，都收敛到同一序列。
- `Apply` 幂等；`Merge` 支持乱序到达的操作日志（按因果序自动重放）。

## 结构

- `rga.go` — RGA 核心实现（`New` / `InsertAfter` / `Apply` / `Merge` / `String` / `IDAt` / `Ops`）
- `rga_test.go` — 验收与收敛性测试
- `example/main.go` — 双人协同编辑运行样例

## 运行样例

```console
$ go run ./example
initial: user1="AB" user2="AB"
offline:  user1="AXB" user2="AYB"
merged:   user1="AYXB" user2="AYXB"
converged: both replicas show the same document
```

场景：文档为 `AB`，用户 1 在 `A` 后插入 `X`，用户 2 并发在 `A` 后插入 `Y`。
合并后两端稳定显示 `AYXB`（本例中 Y 的 ID 更大故排前；若 ID 次序相反则为 `AXYB`），
绝无字符交错或乱序。

## 测试结果

```console
$ go test ./... -v
=== RUN   TestConcurrentInsertAtSamePosition
    rga_test.go:67: converged to "AYXB" on both replicas
--- PASS: TestConcurrentInsertAtSamePosition (0.00s)
=== RUN   TestConcurrentInsertDeterministicOrder
=== RUN   TestConcurrentInsertDeterministicOrder/swapDelivery=false
=== RUN   TestConcurrentInsertDeterministicOrder/swapDelivery=true
--- PASS: TestConcurrentInsertDeterministicOrder (0.00s)
    --- PASS: TestConcurrentInsertDeterministicOrder/swapDelivery=false (0.00s)
    --- PASS: TestConcurrentInsertDeterministicOrder/swapDelivery=true (0.00s)
=== RUN   TestMergeUnordered
--- PASS: TestMergeUnordered (0.00s)
=== RUN   TestApplyIdempotent
--- PASS: TestApplyIdempotent (0.00s)
=== RUN   TestApplyMissingParent
--- PASS: TestApplyMissingParent (0.00s)
=== RUN   TestRandomizedConvergence
    rga_test.go:210: all 5 replicas converged to "hmugjtsnteceljwzwqbjfjkibyasrezqfkrotegamjaprkjyhcwecbvgvrcgblntftmapeaeratgnbyljdwsosnnffofcwofomkpjecss"
--- PASS: TestRandomizedConvergence (0.00s)
PASS
ok  	rga	0.331s
```
