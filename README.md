# bitmapsched — 压缩位图 + 多租户 DRR 公平调度器 (Go)

纯标准库实现，包含两个独立组件：

- `bitmap`：Roaring 风格压缩位图。32 位键空间按高 16 位切分为容器，每个容器在
  **ArrayContainer**（有序数组，≤4096 个值）、**BitmapContainer**（64Kbit 位图）、
  **RunContainer**（RLE 区间编码）之间自动选择最紧凑编码；支持 AND / OR / XOR
  及 `AndAll` / `OrAll` / `XorAll` 批量运算。
- `scheduler`：多租户任务调度器。每租户独立 FIFO 队列实现资源隔离，
  **Deficit Round Robin** 加权公平排队（按 `Weight` 比例分配服务），
  配额限制（`MaxQueue` 背压拒绝 + `MaxConcurrent` 并发上限），
  `Cost` 感知计费，任务 panic 隔离，`Stop` 排空队列且严格无 goroutine 泄漏。

## 运行

```sh
go test ./...          # 全部单元测试
go test ./... -race    # 竞态检测（并发安全性）
go test ./bitmap/ -run xxx -bench Large   # 大规模交并异或基准
go run ./cmd/demo      # 运行样例
```

## 设计要点

**容器自动转换**

- 点插入从 ArrayContainer 开始，超过 4096 个值自动提升为 BitmapContainer。
- `AddRange` 直接生成 RunContainer；`Optimize()` 按
  `min(2·card, 8192, 4·runs)` 重新选择最优编码（如稠密位图 → 单区间 RLE）。
- 集合运算按容器类型分派快速路径：位图-位图走 64 位字运算，
  RLE-RLE 走区间级交/并/对称差（XOR 用区间减法合成，不物化元素），
  混合类型走"遍历小集合、探测大集合"的通用路径。

**十亿级模拟**：两个覆盖整个 32 位空间（42.9 亿 ID）的位图，交/并/异或的代价
为 O(容器数) 而非 O(基数)，在超售测试机（loadavg≈200）上为毫秒级，
正常硬件上为微秒~亚毫秒级。

**DRR 公平性**：每轮为每个活跃租户充值 `Weight × Quantum` 的赤字额度，
按任务 `Cost` 扣减；每次持锁只跑一轮，避免各租户在同一轮内顶满相同的
并发上限而抹平权重比例；赤字设有上限防止被阻塞租户无限积累信用。

## 样例输出（go run ./cmd/demo）

```
=== Compressed bitmap demo ===
sparse 100k random IDs : containers array=51267 bitmap=0 run=0 cardinality=99998
dense [10M,20M) range  : containers array=0 bitmap=0 run=154 cardinality=10000000
AND |x|=3.5e9 |y|=3.29e9 -> |inter|=2500000000 in 4.227583ms
OR  -> |union|=4294967296 in 56.916333ms
XOR -> |diff|=1794967296 in 13.414292ms
batch AndAll([0,1000),[500,1500),[900,2000)) -> [900 901 ... 999]
batch OrAll cardinality -> 2000

=== Multi-tenant DRR scheduler demo ===
mid-stream (weights 1:3): analytics=82 billing=191 ratio=2.33
tenant analytics submitted=300 completed=300 rejected=0 inflight=0 queued=0
tenant billing   submitted=300 completed=300 rejected=0 inflight=0 queued=0
```

## 测试结果（go test ./... -race）

```
ok  	bitmapsched/bitmap
ok  	bitmapsched/scheduler
```

测试覆盖：

- `bitmap`：稀疏插入→Array、稠密区间→RLE、4096 阈值转换、Optimize 重编码、
  RLE 相邻合并、AND/OR/XOR 对照 map 参考实现（随机用例×20）、批量运算、
  全 32 位空间（42.9 亿 ID）、十亿级交并异或正确性与耗时、空集/不相交边界、
  运算结果紧凑性。
- `scheduler`：权重公平性（1:3 实测比例 3.00）、防饿死（洪泛租户下小租户按时完成）、
  队列配额背压（ErrQuotaExceeded 计数精确）、MaxConcurrent 隔离（阻塞租户不拖垮他人、
  并发上限永不越界）、16 路并发提交无丢失（-race）、成本感知（Cost 10 vs 1 的
  完成顺序 18:4）、未知租户/停止后提交错误、任务 panic 隔离、Stop 排空队列、
  三轮启停后 goroutine 数回归基线（严格无泄漏）。
