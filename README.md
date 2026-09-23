# objectgraph — 有界对象图编解码器（Go 标准库）

一个自包含的对象图序列化/反序列化库，面向不可信输入。解码按**深度、节点数、
引用数、总字节**共同设限；循环引用通过 SCC 缩点定义深度，不会无限增加深度；
任何错误都不会向调用方泄漏部分构造的对象。

- 语言：Go（module `objectgraph`，仅依赖标准库）
- 代码：`objectgraph/`
- 可运行样例：`cmd/objectgraph-demo`
- 测试：`go test ./...`（含 `Example*` 文档样例与 `FuzzDecode` 模糊测试）

## 支持的序列化范围

实体只有 7 种，刻意保持最小，解码端**没有任何机制执行宿主代码**（无函数、
接口、导入、反射式实例化或类型标签驱动的代码执行）：

- 叶子：`null`、`bool`、`int64`（zigzag varint）、`float64`（8 字节大端）、`string`
- 容器：数组（按引用存元素）、对象（字符串键 + 按引用存值）

实体在一张节点表里按索引编号，边可以指向**任意**节点（含前向引用），因此
天然支持共享节点与循环图。线格式见 `objectgraph/format.go`。

## 关键性质如何落实

- **共同限额**（`objectgraph/limits.go`）：`MaxBytes / MaxNodes / MaxRefs /
  MaxDepth / MaxCyclicNodes / MaxPendingRefs / MaxContainerLen` 全部独立强制。
  `MaxRefs` 逐条计数，所以“小输入、大量引用”（例如自引用重复上千次）也受限。
- **先验证长度再分配**：声明的字符串/数组/对象长度先与限额及剩余字节比较，
  通过后才分配后备切片；节点表也在 `nodeCount` 校验后才分配。
- **深度定义**（`objectgraph/analyzer.go`）：取根可达子图的 **SCC 缩点 DAG
  最长路径上的顶点数**。整个图是一个 SCC 时深度为 1；循环不会让深度无限增长。
  SCC 是结构性质，结果与 DFS 顺序无关（有重编号测试 `TestDepthIndependentOfOrdering`）。
- **循环节点单独配额**：属于非平凡 SCC（多节点，或单节点带自环）的不同实体
  数量计入 `MaxCyclicNodes`，与普通节点额度分开。
- **共享节点只计一次**：实体数按不同节点统计；发布时共享节点只构造一个
  `*Value`，边只持有指针（指针同一性测试见 `TestArrayObjectAndSharedNode`）。
- **前向引用暂存有上限**：增量解码时，指向尚未解析记录的边进入 pending 计数，
  超过 `MaxPendingRefs` 立即报错；目标记录出现时即解析，解析结束必须归零。
- **全部验证后原子发布**：先完成字节解析与独立图分析（可达性、SCC、深度、
  循环配额），全部通过才一次性物化 `*Value`；任何错误返回 `nil` 对象，
  `errors.Is` 可判定具体原因。
- **独立图分析器**：`AnalyzeGraph(adj, root)` 可对任意邻接表做可达性/SCC/
  深度分析，全部遍历为迭代式（5 万节点长链测试不会爆栈）。
- **不可达拒绝**：从根不可达的节点判为畸形（`ErrUnreachable`）。
- **限额可恢复**：`Decoder` 无每次调用状态，一次超限失败不“中毒”，下一次
  调用仍拥有完整额度（`TestQuotaRecoversAfterFailure`）。

## 线格式（版本 1）

```
header := 'O' 'G' '1' ver(=1) nodeCount(uvarint) rootIndex(uvarint)
record := tag payload            # 记录按节点索引 0..nodeCount-1 排列
tag    := 0|null 1|bool 2|int 3|float 4|string 5|array 6|object
bool   := byte(0|1)
int    := zigzag varint
float  := 8 bytes big-endian
string := byteLen(uvarint) rawBytes
array  := len(uvarint) { targetIndex(uvarint) }*
object := fieldCount(uvarint) { key(string) targetIndex(uvarint) }*
```

`targetIndex` 必须 `< nodeCount`；尾部多余字节判为非法。

## 快速使用

```go
b := objectgraph.NewBuilder()
n0 := b.Declare()                 // 预留索引，便于建环
n1 := b.Declare()
_ = b.SetObject(n0, []objectgraph.KV{{Key: "next", Ref: n1}})
_ = b.SetObject(n1, []objectgraph.KV{{Key: "prev", Ref: n0}})
p, _ := b.Encode(n0)

v, err := objectgraph.Decode(p)   // DefaultLimits；可用 NewDecoder 自定义限额
v.Field("next").Field("prev") == v // true，同实体回边
```

## 运行样例

命令行演示（共享实体、循环、恶意输入拒绝、限额恢复）：

```
$ go run ./cmd/objectgraph-demo
tree: 26 encoded bytes; array len=3; shared entity=true
cycle: decoded back-edge returns same root=true (SCC condensation depth is 1)
hostile: depth-5 chain rejected (ErrDepth), no object returned
hostile: claimed fan-out rejected before allocation (ErrBadLength)
recovery: "still works"
```

`Example*` 样例内置预期输出，由 `go test` 校验：

```
$ go test -v .
=== RUN   ExampleDecode_roundTrip
--- PASS: ExampleDecode_roundTrip (0.00s)
=== RUN   ExampleDecode_cycle
--- PASS: ExampleDecode_cycle (0.00s)
=== RUN   ExampleDecode_rejected
--- PASS: ExampleDecode_rejected (0.00s)
PASS
```

## 测试结果

```
$ go test -race ./...
ok  	objectgraph                          1.97s
?   	objectgraph/cmd/objectgraph-demo     [no test files]
ok  	objectgraph/objectgraph              1.47s

$ go test -cover ./objectgraph/
ok  	objectgraph/objectgraph   0.40s   coverage: 82.5% of statements
```

模糊测试（约 119 万次随机输入，无 panic、无部分对象泄漏）：

```
$ go test -run='^$' -fuzz=FuzzDecode -fuzztime=8s ./objectgraph/
fuzz: elapsed: 9s, execs: 1193933, new interesting: 78 (total: 81)
PASS
```

覆盖要点：标量/数组/对象往返、共享节点指针同一性、二节点环与自环、
深度与遍历顺序无关、5 万节点长链迭代分析、前向引用解析与暂存上限、
小输入大引用预算、节点/循环/深度三类配额、超长字符串与巨型扇出的
“先验证长度”、非法标记/偏移/版本/尾字节/varint 溢出、失败后额度恢复。
