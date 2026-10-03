# prefixseek

支持前缀 Seek 的 LSM 风格 SSTable 索引遍历器（仅 Go 标准库）。

## 设计

- **SSTable 布局**：`[data block]* [index block] [footer]`。
  数据块内条目为 `uvarint klen | uvarint vlen | key | value`；
  索引块每条记录保存「数据块最大 key → (offset, size)」；
  footer 固定 24 字节（indexOffset | indexLen | magic）。
- **快速定位**：`PrefixSeek(prefix)` 在索引块上二分查找第一个
  `lastKey >= prefix` 的数据块，前置不相关 Block 零读取跳过。
- **边界截断**：迭代到第一个不带前缀的 key（如下一租户）立即结束，
  不触碰后续 Block。
- **零拷贝**：`Key()`/`Value()` 返回表镜像的子切片，无任何复制。

## 运行

```sh
go test -v ./...   # 测试（含 60 万条多租户记录的验收用例）
go run ./cmd/demo  # 运行样例
```
