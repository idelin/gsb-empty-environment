# wireparse

一个不信任长度字段的、可暂停的 protobuf 风格二进制 wire 格式**明确子集**解析器。
仅使用 Go 标准库，不依赖任何现成的消息编解码库。

## 格式子集

- key = `(field << 3) | wireType`，varint 编码；字段号合法范围 `1 .. 2^29-1`。
- wire type：
  - `0`：uint64 varint，最多 10 字节（第 10 字节 > 1 判溢出）；
  - `1`：小端 64 位；
  - `2`：varint 长度 + 原始字节；
  - `5`：小端 32 位；
  - 其他类型一律拒绝。

## 解析模型

- `Parser` 是增量解析器：`Feed([]byte)` 接受任意分块，`Next()` 每次产出一个
  `Event`；数据不足时返回 `ErrNeedMore`，可随时暂停/续喂；`Finish()` 标记输入结束。
- `Decode(io.Reader, limits, schema, chunkSize)` 是便捷驱动，返回消息树与 `Stats`。
- `TreeBuilder` 把事件流折叠成 `Message` 树，供自驱动调用方使用。

## 资源限制（分配前检查）

`Limits` 全部维度都在任何相关分配之前检查；0 表示不限：

- `MaxTotalBytes`：所有 `Feed` 累计字节上限；
- `MaxFieldLen`：单个 type-2 字段负载上限（先验长度 varint，再分配）；
- `MaxFields`：整棵消息树字段总数上限；
- `MaxDepth`：嵌套消息深度上限。

声明长度超出外层消息剩余字节时立即报截断，不等待数据到达。

## Schema 与未知字段

`Schema` 显式声明哪些 type-2 字段解释为嵌套消息（`KindMessage`）或 packed 数组
（`KindPacked`，元素类型 0/1/5）；未声明字段保留原始字节。未知字段按原出现顺序
保留在 `Message.Fields` 中，重新编码（`Message.Encode`）不会丢弃。

packed 固定宽度负载长度必须整除元素宽度，否则拒绝；packed varint 元素逐个做
溢出/截断检查。

## 错误

所有解析失败返回 `*wire.Error`，包含：

- `Code`：overflow / field-number / wire-type / truncated / limit / packed；
- `Offset`：出错字节的全局偏移；
- `Path`：从根消息到出错字段的字段号路径（如 `5.1`）。

## 资源峰值记录

`Parser.Stats()` 返回：已喂入/已消费字节、字段数、嵌套消息数、内部缓冲区
高水位（`PeakBufferBytes`）。CLI 额外用采样协程记录进程堆峰值
（`peak_heap`）与累计分配（`total_alloc`）。

## 测试

```
go test ./...
```

独立字节夹具（`fixtures_test.go`，手写字节而非用被测编码器生成）覆盖：
最大 uint64、空长度字段（raw/嵌套/packed）、重复字段、最大字段号、
对组合消息做**每个字节截断**（非字段边界的截断必须报 truncated，边界截断
必须重编码回自身）、**逐字节喂入**与批量解码结果一致、varint 溢出
（key/length/value）、非法字段号、非法 wire type、packed 不整除与截断元素、
全部四类限制、错误偏移与字段路径。合法消息验证解码→重编码字节一致，
未知字段不丢失。

## CLI

```
go build ./cmd/wiretool
printf '\x08\x96\x01\x12\x02ab' > msg.bin
./wiretool -schema '5:msg,6:pvarint,7:p32,8:p64' \
    -max-total 1000000 -max-field-len 65536 -max-fields 10000 -max-depth 16 \
    -chunk 4096 msg.bin
./wiretool -mode roundtrip -schema '5:msg' msg.bin   # 重编码并逐字节比对
```

输出字段树到 stdout；`stats:` 行（字段数、消费字节、缓冲区峰值、堆峰值、
累计分配）到 stderr；解析失败时错误（含偏移与字段路径）到 stderr，
退出码非零。
