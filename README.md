# bitmap — 压缩位图：多节点并发竞争与冲突仲裁器（Go 版）

纯标准库实现的 Roaring 风格压缩位图，面向多执行体并发场景，提供原子性、
线性一致性与无死锁/无竞态保证。

## 特性

- **紧凑存储 + 自动容器选择**：每个 16 位键空间按实际内容自动选择最省内存的表示
  - `arrayContainer`：有序 `[]uint16`，稀疏时最优（≤4096 个元素）
  - `bitmapContainer`：定长 65536 位位图，稠密时最优
  - `runContainer`：RLE 区间编码，连续/成簇数据最优（一条区间即可表示整个容器）
  - 转换依据精确的字节数比较（`2*card` vs `8192` vs `4*runs`），增删与集合运算后自动 `optimize()`
- **批量集合运算**：`And` / `Or` / `Xor`，九种容器组合均有专门实现
  （bitmap-bitmap 按字并行、run-run 双指针区间扫描、xor 不物化位图）
- **多节点并发竞争与冲突仲裁**
  - 48 位容器键空间条带化为 256 个分片，结构变更由分片 `RWMutex` 仲裁
  - 单键增删**无锁**：容器写时复制（COW）+ CAS 发布，竞争失败者基于胜者已提交的值重试，CAS 成功点即线性化点
  - 多键 `Commit` / `ContainsAll` / `CountPresent` 按**严格升序**获取分片锁——全局唯一锁序从构造上杜绝死锁；批提交对读者原子可见
  - 位图级 AND/OR/XOR 任意时刻最多持有一个分片锁（快照后无锁计算），不会与写者死锁
- **无泄漏**：不启动任何后台 goroutine（有严格回归测试）

## 并发模型（仲裁器）

```
Node(执行体) ──Add──▶ slot CAS 竞争 ──胜者提交, 败者重试──▶ 线性化点
Node ──Commit(批)──▶ 升序分片写锁 ──原子应用──▶ 读者(升序读锁)只见全或无
```

## 使用

```go
bm := bitmap.New()

// 稀疏离散 ID
bm.Add(42)
bm.Add(1 << 40)

// 密集连续区间（自动存为 RLE，一条区间 = 一个容器）
bm.AddRange(1<<22, 1<<22+999999)

// 多节点竞争：每个执行体一个 Node 句柄
nd := bm.Node(workerID)
nd.Add(id)

// 原子批提交（跨分片线性一致）
b := bm.Batch()
b.Add(x).Add(y).Remove(z)
nd.Commit(b)

// 集合运算
inter := a.And(b)
union := a.Or(b)
diff  := a.Xor(b)
```

## 运行样例

```
$ go run ./cmd/demo
=== 1. 容器自动选择：稀疏ID + 密集区间 ===
cardinality=301622 containers=11 (array=4 bitmap=1 rle=6)

=== 2. 多节点并发竞争：16个执行体同时写入 ===
写入 1616000 个ID（含1000个全节点竞争的热点ID），耗时 295ms
cardinality=1601000 (期望 1601000，严格相等 => 无丢失、无重复提交)

=== 3. 批量原子提交（线性一致性） ===
ContainsAll(批量4个ID)=true CountPresent=4

=== 4. 百亿级数据模拟：两个Bitmap的交/并/异或 ===
A: 10000000000 个元素, 152588 个RLE容器; B: 10000000000 个元素, 152589 个RLE容器
AND: |A∩B|=5000000000  耗时 96ms (平均每容器 629ns)
OR : |A∪B|=15000000000  耗时 320ms
XOR: |A△B|=10000000000  耗时 142ms

=== 5. 稀疏Bitmap交并（100万离散ID × 2） ===
AND: |S1∩S2|=231 耗时 74ms;  OR: |S1∪S2|=1999769 耗时 194ms
```

注：百亿级集合以 RLE 压缩存储（10^10 个 ID 仅 ~15 万个区间容器，内存几十 MB），
单容器对交并运算为亚微秒/微秒级（见基准）。

## 测试

```
$ go test ./... -count=1 -race
ok  	bitmap/bitmap	28.9s
```

覆盖：

- 容器单元：增删查、有序性、Array→Bitmap/RLE 自动转换与回转、run 合并/分裂、全满区间
- 集合运算对拍：三种容器 × AND/OR/XOR × 随机稀疏/稠密/成簇数据 vs map oracle（含输入不可变性）
- 位图级：稀疏+稠密混合、批量提交、CountPresent、ForEach 有序、空位图边界
- 并发安全（`-race`）：
  - 16 节点不相交写入 → 基数严格相等（无丢失）
  - 32 节点竞争**同一批** ID → CAS 仲裁恰好每 ID 一个胜者
  - 单容器热点写风暴 + 并发删除 → 精确算术验证
  - 跨分片批提交原子性：读者 `CountPresent` 永不见半提交批次（0 或 4，绝不 1..3）
  - 并发读写混合、写并发下的 AND/OR/XOR 集合不变量
  - 并发 Commit 无死锁、无更新丢失
  - goroutine 严格无泄漏（前后计数一致）

## 基准（Go 1.11, darwin/amd64）

```
BenchmarkContainerAndBitmapBitmap-10   100000     4947 ns/op     9472 B/op   1 allocs/op
BenchmarkContainerOrBitmapBitmap-10    100000     6204 ns/op     9472 B/op   1 allocs/op
BenchmarkContainerAndRunRun-10        1000000      245 ns/op       56 B/op   3 allocs/op
BenchmarkBitmapAndDense10B-10                 240014500 ns/op  (10^10 元素, 15万容器)
BenchmarkBitmapAndSparse-10            3        73713500 ns/op  (100万离散ID × 2)
BenchmarkConcurrentAdd/nodes=16-10    500000       406 ns/op
BenchmarkAddRange-10                   10000     76089 ns/op   (每百万ID区间)
```

## 结构

```
bitmap/
  container.go   三种容器实现 + 自动转换（chooseKind/optimize）
  setops.go      容器级 AND/OR/XOR（九种组合，纯函数、COW）
  bitmap.go      分片位图、CAS 仲裁、有序锁批提交、Node 句柄
  ops.go         位图级 AND/OR/XOR（分片快照 + 无锁计算）
  *_test.go      单元 / oracle 对拍 / 并发竞争 / 基准
cmd/demo/        运行样例
```
