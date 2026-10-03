# lseq — LSEQ 高效协同序列（Go 版）

LSEQ：基于无界分数因果树（unbounded-score causal tree）的协同序列 CRDT。
每个元素持有一个不可变的致密标识符（树路径），标识符的全序即序列顺序，
因此多副本以任意顺序合并操作后都能确定性地收敛到同一序列。

## 核心设计

- **自适应可变基数**：标识符树的层 arity 随深度翻倍（8, 16, 32, …），
  越深入空间越大，见 `identifier.go` 的 `baseAt`。
- **致密标识符分配**：在两个紧邻位置之间，沿树下行找到首个有空位的层，
  在邻接边界 `boundary` 范围内随机取位，见 `identifier.go` 的 `Allocate`。
- **方向自适应 boundary 策略**：右边缘（q 为虚拟上界）用 boundary+ 贴左邻、
  左边缘用 boundary- 贴右邻、中间位置随机选择。连续输入因此形成远离边界的
  随机游走，深度随编辑数对数增长，避免位宽爆炸。策略只影响标识符大小，
  不影响收敛性。
- **因果序保证**：标识符按 `(Val, Site)` 字典序全序排列，并发同位插入由
  站点号决胜；同一站点的字符在合并后严格保持输入顺序。

## 运行

```sh
go test ./... -v     # 测试（含验收边界）
go run ./cmd/lseq-demo   # 运行样例
```

## 验收结果（实测）

- `TestDenseAlternatingInsert`：开头/结尾 1000 次密集交替插入，
  `maxDepth=9, avgDepth=7.43`（对数级，远低于位宽爆炸阈值）。
- 尾部连续输入深度曲线：n=1→4096 时 maxDepth=1→12，每翻倍仅 +1 层。
- `TestConcurrentMergeCausalOrder`：4 节点并发输入，64 次随机全排列合并
  全部收敛到同一文档，且每节点文本保持严格因果序（保序子序列）。
- `TestCausalDeliveryWithDeletes`：增删混合、按站点因果序随机投递，
  32 次随机交错全部收敛。
- `go test -race -count=5` 通过。
