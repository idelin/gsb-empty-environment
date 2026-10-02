# prefixseek

支持前缀 Seek 的 LSM 风格 SSTable 索引遍历器（仅 Go 标准库）。

## 文件格式

```
[DataBlock]* [IndexBlock] [Footer]
DataBlock  : (uvarint klen | key | uvarint vlen | value)*
IndexBlock : (uvarint klen | sepKey | uvarint offset | uvarint size)*
Footer     : indexOffset u64 | indexSize u64 | magic u64
```

- `Builder.Add(key, value)` 按严格递增序写入，按块大小（默认 4KB）切分数据块；
  每个数据块的最后一个 key 作为索引项的分隔键。
- `Open` 只把 Index Block 读进内存。
- `Reader.PrefixSeek(prefix)` 在索引块上二分定位到第一个 `sepKey >= prefix`
  的数据块，跳过前置不相关 Block；随后顺序解码，`Key()/Value()` 返回
  块缓冲区的子切片（零拷贝）；遇到第一个不带前缀的 key 立即停止
  （边界截断），`Iterator.StopBlock` 记录终止位置。

## 运行样例

```sh
go run ./cmd/demo
```

```
[build] 300005 records in 4.678s
[open ] 5062 data blocks, index block resident in memory
[scan  ] tenant_A:order_000000 => {"order":0,"amount":0.99,"status":"paid"}
[scan  ] tenant_A:order_000001 => {"order":1,"amount":1.99,"status":"paid"}
[scan  ] tenant_A:order_000002 => {"order":2,"amount":2.99,"status":"paid"}
[scan  ] tenant_A:order_059999 => {"order":59999,"amount":999.99,"status":"paid"}
[done  ] PrefixSeek('tenant_A:order_') -> 60000 orders in 124.389ms
[stats ] blocks read: 1013 / 5062, stopped at block #1012 (boundary truncation)
```

## 测试结果

```sh
go test ./... -v
```

```
--- PASS: TestPrefixSeekMultiTenant        # 30 万条记录，tenant_A:order_ 命中 60000 条，仅读 1299/6491 块
--- PASS: TestPrefixSeekSkipsPrecedingBlocks # 中间租户 tenant_C 跳过前置租户数据块
--- PASS: TestPrefixSeekLastTenantStopsAtEOF # 末租户扫描在 EOF 干净终止
--- PASS: TestPrefixSeekNoMatch            # 无匹配前缀最多读 2 块
--- PASS: TestPrefixSeekZeroCopy           # unsafe 指针校验 Key/Value 为块缓冲子切片
--- PASS: TestBuilderRejectsUnsortedKeys   # 非递增 key 拒绝写入
PASS
ok  	prefixseek	5.873s
```
