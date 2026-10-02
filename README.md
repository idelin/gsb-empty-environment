# roaring — 压缩位图：持久化编解码与序列化零拷贝还原 (Go)

一个 Roaring-Bitmap 风格的压缩位图库，仅使用 Go 标准库。支持 32 位整数集合的
紧凑存储、高效布尔集合运算、版本化二进制持久化、零拷贝还原与内存映射解析。

## 特性

- **三种容器自动转换**：按 16 位高位分桶，每个桶根据内容自动选择最优编码
  - `ArrayContainer`：有序 uint16 数组，基数 ≤ 4096 时最省空间（2 字节/值）
  - `BitmapContainer`：固定 8 KiB 位图（1024 个 uint64 字），密集数据按字并行运算
  - `RunContainer`：RLE 区间对 `(start, length)`，连续区间极致压缩
  - 每次运算后按 `min(2·card, 4·runs, 8192)` 重新选择最优编码（`repair`）
- **AND / OR / XOR 集合运算**：两两运算 `And/Or/Xor` + 批量运算 `AndAll/OrAll/XorAll`；
  结果为新位图，输入不可变，可安全并发共享
- **版本化二进制格式**：魔数 + 版本号，向后兼容（旧版本可读）、向前兼容
  （忽略未知 flags/保留字段/尾部扩展字节）
- **零拷贝还原**：`Deserialize` 直接以 `unsafe` 将缓冲区映射为容器切片，
  不复制任何负载；写时复制（COW）保证后续 `Add` 不会污染源缓冲区
- **内存映射解析**：`OpenMapped`（darwin/linux）直接 mmap 文件只读映射解码
- **边界防溢出**：全部偏移/长度以 64 位运算校验，恶意构造的 count、
  cardinality、run 数、截断数据均返回错误而非 panic/溢出
- **并发安全**：只读操作与序列化可并发；`-race` 测试覆盖

## 二进制格式 (version 1, little-endian)

```
offset  size  field
0       4     magic "ROAR" (0x524F4152)
4       2     format version（解码器接受任何 <= 当前版本的数据）
6       2     flags（保留，解码忽略 → 向前兼容）
8       4     容器数量 N
12      4     reserved（保留，解码忽略）
16      8·N   容器头（按 key 升序）：
              key u16 | type u8 | reserved u8 | cardinality u32
16+8N   ...   容器负载，每个 8 字节对齐：
              array : cardinality × u16（严格递增）
              bitmap: 1024 × u64（8192 字节）
              run   : u32 runCount + runCount × (start u16, length u16)
...     ...   允许尾部扩展字节（解码忽略 → 向前兼容）
```

## 用法

```go
bm := roaring.New()
bm.Add(42)                        // 稀疏离散 ID
bm.AddRange(1_000_000, 2_000_000) // 密集连续区间（自动选 RunContainer）

inter := a.And(b)                 // 交
union := a.Or(b)                  // 并
diff  := a.Xor(b)                 // 对称差
all   := roaring.AndAll(x, y, z)  // 批量

buf, _ := bm.Serialize(), error   // 持久化
back, err := roaring.Deserialize(buf) // 零拷贝还原（共享 buf 内存）

m, err := roaring.OpenMapped(path)    // mmap 只读映射解析
defer m.Close()
```

## 运行

```sh
go test ./...          # 全部测试（含边界/损坏数据/并发/泄漏断言）
go test -race ./...    # 竞态检测
go test -bench .       # 基准
go run ./cmd/demo      # 端到端样例
```

## 测试覆盖

- 稀疏/密集/混合数据与参考实现（`map[uint32]bool`）交叉验证 AND/OR/XOR
- 容器组合矩阵：array/bitmap/run 三三组合的全部运算
- 容器自动选择：4096/4097 阈值提升、连续区间选 run、运算后重选
- 边界值：0、65535、65536、max uint32、跨容器区间、全容器区间
- 序列化往返：空图、单值、各容器类型、混合、全 key 空间尾部
- 零拷贝：指针级别名断言 + 修改源缓冲区可见性验证 + COW 隔离验证
- 损坏数据：逐字节截断、坏魔数、非法类型/基数/run 数、key 乱序、
  区间重叠、基数不符、2000 组随机字节（不 panic）
- 兼容性：version 0 可读、version 2 拒绝、未知 flags/保留字段/尾部字节忽略
- 并发：32 goroutine 混合只读运算（`-race`）、mmap 并发读取
- 无泄漏：finalizer 断言全部位图被 GC 回收 + goroutine 数不增长

## 基准（Go 1.11, darwin/amd64；数值随机器负载波动）

| 场景 | 规模 | 耗时 |
|---|---|---|
| `And` 混合容器（array+bitmap+run） | 2×64 容器 | **~9 µs/op** |
| `And` 全 key 空间（run 编码） | 2×2³² 值（≈86 亿逻辑位） | ~45–110 ms/op |
| `And` 百亿级逻辑批量模拟 | 3 对全 key 空间（≈2.6×10¹⁰ 逻辑位） | ~160–300 ms/op |
| `And` 密集位图容器 | 2×4096 容器（≈2.7 亿值/图） | ~210 ms/op |
| `Or`/`Xor` 密集位图容器 | 同上 | ~200–400 ms/op |
| 零拷贝 `Deserialize` | 512 容器 / 4 MiB | ~2–3 ms/op |
| `Serialize` | 512 容器 / 4 MiB | ~7–13 ms/op |
| `AddRange` 连续区间 | 2²⁸ 个值 | ~2 ms/op |
| `Contains` | 随机点查 | ~140–220 ns/op |

说明：不可变设计下运算结果需新分配内存，密集位图大运算的主要开销是结果
分配（每容器 8 KiB）；真实分片场景（数十容器）的交并运算为微秒级。

## 并发契约

- `Add`/`AddRange` 原地修改，非并发安全；共享前请 `Clone()`
- `And/Or/Xor/Contains/Serialize/Stats` 为只读操作，可并发
- `Deserialize`/`OpenMapped` 返回的位图与底层缓冲区共享内存：
  缓冲区须保持存活且不可修改；对零拷贝位图的首次写入会触发该容器的
  写时复制，不会污染源缓冲区
