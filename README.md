# idset

`idset` 是一个 32 位无符号 ID 的压缩集合（类似 Roaring Bitmap）。按高 16 位分块，
每块的低 16 位使用三种容器之一：

| 容器 | 表示 | 序列化大小 |
|------|------|-----------|
| array | 排序 `[]uint16` | `2 * count` 字节 |
| bitmap | 固定 2^16 位图 | 8192 字节 |
| run | 互不相邻的闭区间 `[lo,hi]` | `4 * runs` 字节 |

## 规范性（canonical）

容器的规范形式取**实际序列化字节数最小**者；平局按固定优先级
**array > run > bitmap**。更新（Add/Remove/集合运算）后允许延迟转换，
`Optimize()` 可立即规范化；`Encode()` 总会在输出前规范化，因此同一逻辑
集合的编码字节唯一。空块不出现在编码中。

## API

- `Add` / `Remove` / `Contains` / `Size`
- `And` / `Or` / `Xor`：按块归并，逐值合并容器，最多只展开一个 2^16 位块
  （8KB），不会把全 32 位域展开成巨大数组
- `Rank(x)`：≤ x 的元素数；`Select(k)`：从 0 开始的第 k 项，越界返回
  `ErrSelectRange`
- `Snapshot()`：O(1) 快照。容器不可变 + 顶层切片写时复制，快照后修改
  互不影响
- `Encode()` / `Decode([]byte)`：规范编码与独立校验解码器

## 编码格式（小端）

```
uint32 numBlocks                      // 块的高16位键严格递增
每块:
  uint16 highKey
  uint8  containerType                // 0=array 1=bitmap 2=run
  uint32 valueCount
  payload:
    array:  valueCount × uint16       // 严格递增
    bitmap: 8192 字节                 // popcount == valueCount
    run:    uint32 numRuns, 然后 numRuns × (uint16 lo, uint16 hi)
            // lo<=hi，区间不相交且不相邻，长度之和 == valueCount
```

`Decode` 独立校验：容器计数、数组排序、run 重叠/相邻、块键顺序、
空块、未知类型、尾部垃圾，以及容器类型是否为规范选择。

## 测试

```
go test ./...
```

覆盖：65535/65536 边界、全块、空块、run 拆分/合并、转换平局
（array↔bitmap、array↔run、run↔bitmap）、三种容器 3×3 组合的
AND/OR/XOR、快照隔离、解码器全部拒绝路径，以及以 `map[uint32]`+排序
为参考的随机模糊对照。`TestEncodingSizes` 打印各形态的真实编码尺寸。
