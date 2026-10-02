# Roaring Bitmap 位图压缩索引器

高性能稀疏/稠密自适应位图索引，纯 Go 标准库实现。

## 设计

- 32 位整数按**高 16 位**分块（Chunk），块按键有序、二分定位。
- **低 16 位**在块内自适应存储：
  - 稀疏（基数 ≤ 4096）：有序数组（Array Chunk），二分查找/插入。
  - 稠密（基数 > 4096）：自动升级为 1024 个 `uint64` 的位图（Bitset Chunk）。
- 交集/并集在块级别进行：
  - Array ∩ Array：有序数组合并。
  - Bitset ∩/∪ 任意：按 64 位字做常数级位运算，并用 `math/bits` 计数。
  - 运算结果按基数自适应收缩（Bitset → Array）或升级（Array → Bitset）。

## 运行

```sh
go test ./... -v        # 测试
go run ./cmd/demo       # 运行样例
```
