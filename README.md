# omset — 有序多重集（压缩有序索引）

纯标准库 Go 实现。按值存有序计数与累计重数（前缀和），支持重复元素选择、
批量原子加减、相交（取最小重数），计数永不为负。

## 内存布局

三条并行切片，始终保持同步：

| 字段      | 类型       | 含义                                   |
|-----------|------------|----------------------------------------|
| `values`  | `[]int64`  | 去重后的值，严格升序                   |
| `counts`  | `[]uint64` | `values[i]` 的重数，恒 >= 1            |
| `prefix`  | `[]uint64` | 累计重数 `prefix[i] = Σ counts[0..i]`  |

`Select(k)` 在 `prefix` 上二分，`Rank(v)`（<= v 的累计重数）同样 O(log n)。

## 二进制编码布局（`Encode` / `SizeBytes`）

不是 map 序列化，而是 delta + varint 的压缩布局：

```
uvarint  n                        distinct 值个数
n × { varint  valueDelta          zigzag(value[i] - value[i-1])，首项为 zigzag(value[0])
      uvarint count }             该值重数
n ×   uvarint prefixDelta         prefix[i] - prefix[i-1]，首项为 prefix[0]
```

`SizeBytes()` 不分配内存即可给出精确字节数（测试断言与 `len(Encode())` 相等）。
示例：`[1,1,3]` 编码为 `02 02 02 04 01 02 01`，共 **7 字节**
（n=2；值 delta 1、2 各 1 字节；计数 2、1；前缀 delta 2、1）。
1 万个元素、100 个 distinct 值时编码 < 1 KiB（见 `TestEncodingIsCompact`）。

## 原子性

`Apply(batch ...Delta)` 两阶段：先按值聚合净增量并对照当前计数校验，
任一值会变负则返回 `ErrNegativeCount`，集合完全不变；全部合法才提交。

## 运行

```sh
go test -v ./...
go run ./cmd/demo
```
