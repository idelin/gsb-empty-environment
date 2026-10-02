# Roaring Bitmap（Go 标准库实现）

高性能稀疏/稠密自适应位图压缩索引。

## 设计

- 32 位值按**高 16 位分块**（Chunk），块内**低 16 位**自适应选择容器：
  - **Array Chunk**：稀疏场景，有序 `[]uint16`，二分查找，容量上限 4096。
  - **Bitset Chunk**：稠密场景，定长 `[1024]uint64`（65536 位），O(1) 置位/查询。
- **自适应转换**：Array 超过 4096 元素自动升级为 Bitset；Bitset 基数回落到 4096 及以下
  （删除或 AND 运算后）自动收缩为 Array。
- **集合运算**：`And` / `Or` 按 chunk key 归并，块内按两侧类型分派：
  - Array∩Array：双指针归并；Array∩Bitset：逐位查询；Bitset∩Bitset：64 位字并行 AND/OR。

## API

```go
b := roaring.New()
b.Add(42)                 // 插入
b.AddRange(10000, 15000)  // 连续区间插入
b.Remove(42)              // 删除（自动收缩）
b.Contains(42)            // 查询
b.Cardinality()           // 基数
b.ToSlice()               // 升序导出
b.ChunkType(0)            // 观测分块类型："array" / "bitset"

and := roaring.And(a, b)  // 交集（结果分块自适应）
or  := roaring.Or(a, b)   // 并集（结果分块自适应）
```

## 运行

```sh
go test -v ./...   # 测试
go run ./cmd/demo  # 运行样例
```
