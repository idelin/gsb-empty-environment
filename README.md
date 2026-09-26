# bitmapdemo — 持久化位图（块级 COW + 懒物化 + 引用回收）

纯 Go 标准库实现。位图按 **4096 位 / 512 字节** 分块，快照共享未修改的块，
写入时对目标块做写时复制；`And`/`Or` 生成懒节点，块在首次访问时才物化；
整棵图用引用计数管理，释放到 0 时递归回收。

## 内存布局

```
Bitmap  = 头部 { refs, nbits, op, dir[], mat[], left, right }   (unsafe.Sizeof 实测)
dir     = 块目录，每槽 8 字节指针，nil 表示全零块
block   = [64]uint64 = 512 字节，快照间共享，写时复制
mat     = 懒节点专用，每槽 1 字节，标记该块是否已物化
```

- 快照成本 = 目录拷贝（`块数 × 8` 字节）+ 每块引用计数 +1，零数据块拷贝。
- 一次翻转的成本 = 恰好一个 512 字节块的拷贝（仅当块被共享时）。
- `MemStats()` 按真实图去重统计头部 / 目录槽 / 块，给出占用字节。

## 磁盘编码布局（Encode / Decode，小端）

```
offset  size            field
0       4               magic "BMC1"
4       8               nbits   uint64
12      4               blocks  uint32  (目录长度)
16      4               nonzero uint32  (非空块数)
20      8 * blocks      目录: uint64 负载偏移, 0 = 空块
20+8n   512 * nonzero   块负载, 按目录顺序
```

空块只占一个 8 字节目录项。1 Mi 位、3 个脏块的位图编码为
`20 + 256×8 + 3×512 = 3604` 字节（裸位图需 131072 字节），
`EncodedSize()` 在写出前即可给出精确字节数。

## 语义保证

- **快照隔离**：`Snapshot()` 之后对原图的翻转不影响快照，反之亦然（块级 COW）。
- **懒物化**：`And`/`Or` 返回时不计算任何块；`Get` 只物化所在块，`Count`/`Encode` 强制全量物化。
- **引用回收**：`Release()` 减到 0 递归释放子引用；重复 `Release` 是空操作，计数永不为负。
- **长度预检**：`And`/`Or` 操作数长度不一致时在创建任何节点/引用之前返回
  `ErrLengthMismatch`（可用 `errors.Is` 判定）。

## 运行

```sh
go test ./...     # 运行全部测试
go run ./cmd/demo # 运行样例
```

## 样例输出（go run ./cmd/demo）

```
block geometry: 4096 bits/block, 512 bytes/block, 256 blocks total

a: 3 bits set, 3 non-empty blocks
snapshot after flips of a: count=3 (unchanged), a.count=3
shared graph: 1 headers + 3 blocks + 256 dir slots = 3672 bytes live

lazy AND created: materialized blocks = 0 (nothing computed yet)
AND.Get(8)=true AND.Get(5000)=true -> materialized blocks = 2
AND popcount (forces full materialization) = 2, blocks = 256

mismatched AND rejected upfront: bitmap: operands have different lengths (1048576 vs 1024 bits)

encoding of a: 3604 bytes = 20 header + 256*8 directory + 3*512 payloads
EncodedSize() agrees: 3604 bytes (vs 131072 bytes as a raw bitset)

live blocks before release: 9
live blocks after release:  0 (double release kept refs >= 0)
```

## 测试结果（go test ./bitmap/ -v）

```
=== RUN   TestSnapshotIsolation            --- PASS   旧快照不随新翻转变化
=== RUN   TestDoubleReleaseNeverNegative   --- PASS   重复释放引用不为负
=== RUN   TestLengthMismatchRejected       --- PASS   不同长度二元操作预先拒绝
=== RUN   TestLazyMaterialization          --- PASS   懒物化按需进行
=== RUN   TestBlockSharingAndCOW           --- PASS   快照共享块、翻转只复制一块
=== RUN   TestReferenceReclamation         --- PASS   释放后存活块回到基线
=== RUN   TestAndOrCorrectness             --- PASS   逐位校验 AND/OR 语义
=== RUN   TestEncodeLayoutAndRoundTrip     --- PASS   编码字节数精确且可往返
PASS
ok  	bitmapdemo/bitmap
```
