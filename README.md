# invindex — 紧凑只读倒排索引段

纯 Go 标准库实现的文本关键词只读倒排表压缩段。

## 结构

- **词典**：词项按字典序排序，分块前缀压缩（每块 16 个词项，块首词项进入稀疏块索引）。
  查找时先二分块索引，再在块内解压扫描。
- **倒排链（Posting List）**：文档 ID 差值（delta）编码 + 词频，均用 varint 压缩；
  每 √n 个 posting 记录一条跳跃指针（绝对 docID + 字节偏移）。
- **交集求值**：多词 AND 查询由最短倒排链驱动，其余链通过 `Advance` 借助跳跃指针
  快速跳过不可能命中目标 docID 的无用区段。

## 文件

- `segment.go` — 构建器、前缀压缩词典、段布局
- `postings.go` — 差值/varint 倒排链编码、跳跃表、迭代器（`Next` / `Advance`）
- `query.go` — 短链驱动的跳跃指针交集（`And` / `AndStats`）与朴素归并基线（`AndNaive`）
- `cmd/demo/main.go` — 运行样例：`database AND storage`，两词倒排链长度悬殊（25 vs 20000）

## 运行

```sh
go test ./...        # 测试（含验收场景）
go test -v ./...     # 查看段大小、跳跃加速等日志
go run ./cmd/demo    # 运行样例
```
