# blockindex — 带结构摘要与局部证明的块索引

纯 Go 标准库实现。索引把有序键值对打包成定容**块（block）**，再在块上构建
**摘要树（summary tree）**。每个内部节点的摘要提交（SHA-256）其孩子的
**顺序、键范围、条目计数与块计数**；根摘要 `RootSummary` 是验证者唯一需要的
可信承诺。证明是局部的：点查询证明只携带 **1 个数据块** + 路径上各层节点的
孩子列表，验证者不需要加载索引的其余部分。

## 编码布局（真实二进制格式，非 map 包装）

所有整数为 uvarint（`encoding/binary`）。

```
块 "BLK1":  magic4 | version | count | firstKey |
            count * { keyDelta, valueLen, value }
            （首项 keyDelta=0；键严格递增，增量编码）

节点 "SUM1": magic4 | version | level | nChild |
            nChild * { minKeyDelta, maxKeyDelta, entries, blocks, digest[32] }
            （minKeyDelta 相对上一孩子；孩子范围严格递增不重叠）

digest = SHA-256(序列化字节)   —— 版本、计数、范围、孩子顺序全部被绑定
```

实测占用（demo：200,000 条，键距 10，值 8 字节，块容 64，扇出 8）：

| 项目 | 字节 |
|---|---|
| 块数据合计 | 2,028,100（10.14 B/条；定长朴素布局需 16 B/条） |
| 摘要树合计 | 139,876（块字节的 6.9%） |
| 最大块 | 649 |
| 点证明 | 1,977（上界 3,159） |
| 范围证明（501 条结果，9 块） | 17,839 |

精确布局由 `TestBlockLayoutExact` / `TestNodeLayoutExact` 逐字节钉死
（例如 3 项块占 17 字节，2 孩子节点占 79 字节）。

## 证明类型与验证

- **成员证明** `VerifyMembership(root, key, proof)`：返回值与 **rank**
  （小于 key 的键数，由路径上左兄弟的提交计数累加得到），绑定位置与值。
- **非成员证明**：同一证明对象；验证者要求 key 落在已提交的间隙中——
  要么在块内范围中缺席，要么小于路径右兄弟提交的最小 `MinKey`
  （后继块首键），要么大于左兄弟提交的最大 `MaxKey`（前驱块末键）。
- **select** `VerifySelect(root, rank, proof)`：按聚合计数定位第 rank 项。
- **范围证书** `VerifyRange(root, lo, hi, proof)`：返回的块必须在提交的
  块序中**连续**，首块的前驱 `MaxKey < lo`，末块的后继 `MinKey > hi`，
  因此**遗漏任何块都会被拒绝**；空结果必须附边界块证明。

## 证明长度与验证访问界

扇出 F、块数 B、最大块字节 M 时（`bounds.go`）：

```
height        = ceil(log_F B)（非空至少 1）
点证明字节    ≤ 3·10 + M + height·(34 + 78·F)     // O(F·log_F B)
验证读取      = 1 个数据块 + ≤ height·F 个孩子摘要，0 次索引访问
范围证明      = 每个相交块一份点证明（与结果大小成正比，与索引大小无关）
```

实测（`go test -run TestProofSizeAndAccessBounds -v`）：

```
blocks=1    height=1 proof<=682B  (worst 105B)  children<=8  blocks-read=1
blocks=64   height=2 proof<=1303B (worst 679B)  children<=16 blocks-read=1
blocks=500  height=3 proof<=1924B (worst 1008B) children<=24 blocks-read=1
blocks=5000 height=5 proof<=3164B (worst 1464B) children<=40 blocks-read=1
```

## 篡改测试（`tamper_test.go`，全部必须被拒）

对小结构枚举 25+ 种篡改：路径各层兄弟摘要翻转、**仅元数据计数**（entries/
blocks、块内 count 字节）、孩子范围扩张、孩子顺序交换、证明/节点/块版本、
块内容字节、重编码移位键、块位置（BlockIndex）、路径截断/延长、子索引错位；
另有跨版本根拒绝、非成员证明弱化后继界、范围证书遗漏首/中/末块、块序交换、
空结果缺边界证明、边界证明复用于未排除区间。

## 运行

```sh
go test ./...        # 全部测试
go test -v -run TestProofSizeAndAccessBounds .   # 打印证明长度/访问界
go run ./cmd/demo    # 构建 20 万条索引并演示各类证明与篡改拒绝
```

测试结果：15 个测试全部通过（布局精确、解码错误、小结构枚举、全区间
穷举、空索引、大索引抽查、证明界、篡改系列）。
