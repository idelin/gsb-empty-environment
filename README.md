# 紧凑只读倒排索引段（Go，仅标准库）

文本关键词只读倒排表压缩段的构建与检索实现。

## 结构

- **词典**：按块前缀压缩（front coding），每 16 个词一块，块首词建索引；
  查找 = 块索引二分 + 块内顺序扫描。见 `segment/builder.go`、`segment/segment.go`。
- **倒排列表**：`(docID 差值, 词频)` varint 编码；每 8 条 posting 一个跳跃指针
  （记录前一块末位 docID 作为解码基准 + 字节偏移）。
- **交集求值**：短链驱动，长链用 `Advance` 借助跳跃指针整段跳过无用区间。
  见 `segment/intersect.go`。

## 运行样例

```sh
go run ./cmd/demo
```

输出（两链长度悬殊：20 vs 10000，长链仅解码 80 条即完成交集）：

```
segment size: 101051 bytes, terms: 6
posting lists: database=20 docs, storage=10000 docs

query: database AND storage -> 20 hits in 22.292µs
docs: [0 500 1000 1500 2000 2500 3000 3500 4000 4500 5000 5500 6000 6500 7000 7500 8000 8500 9000 9500]

long list 'storage': decoded 80/10000 postings, skip jumps: 19
short list 'database': decoded 20/20 postings
(naive merge would decode all 10000 long-list postings)
```

## 测试

```sh
go test ./segment/ -v
```

覆盖：交集正确性、跳跃指针加速（解码量 ≪ 链长）、词典查找、词频、
`Advance` 与线性扫描对拍（随机数据）、缺失词/空交集边界、段压缩率。
