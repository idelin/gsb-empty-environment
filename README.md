# parquetcol

纯标准库实现的 Parquet v1 列块编码包，面向带可选/嵌套字段的入仓记录。无任何外部依赖，
包含完整的写入路径、读取路径，以及一个手写的 Thrift Compact 协议编解码器。

## 能力

- **Dremel 拆解/装配**：按重复级别（repetition level）与定义级别（definition level）
  递归 shred 记录，并在读回时按相同规则 assemble。
- **嵌套结构**：可选/必填组、重复原语、标准三层 `LIST` 结构（`ListOf`），支持任意层级嵌套。
- **空值语义**：空列表（`List([])`）、空结构（`Struct({})`）、null 列表/结构均可往返。
  空结构由内部合成哨兵列承载定义级别，对调用者透明。
- **级别与索引编码**：RLE / 位打包混合编码（hybrid v1），LSB-first 位打包，自动选择位宽。
- **数据页编码**：`PLAIN` 与 `PLAIN_DICTIONARY` / `RLE_DICTIONARY`。字典在整个列块
  构建前预算，超过 `MaxDictionaryBytes` 时整个列块回落到 PLAIN，不会出现混合页。
- **元数据与统计**：写出 `FileMetaData`、行组、列块、页头，以及每列的 `null_count`、
  `distinct_count`、`min`、`max`（BYTE_ARRAY 统计不含 PLAIN 长度前缀）。
- **物理类型**：BOOLEAN、INT32、INT64、FLOAT、DOUBLE、BYTE_ARRAY（含 UTF8 注解）。
- **多页与多行组**：按目标页大小切分数据页；`Flush` 可显式结束行组。

## 快速上手

```go
schema, _ := parquetcol.Compile(parquetcol.Group("root", parquetcol.Required,
    parquetcol.Leaf("id", parquetcol.Required, parquetcol.TypeInt64),
    parquetcol.ListOf("tags", parquetcol.StringLeaf("element", parquetcol.Optional)),
    parquetcol.Group("meta", parquetcol.Optional,
        parquetcol.Leaf("score", parquetcol.Optional, parquetcol.TypeDouble),
    ),
))

var buf bytes.Buffer
w, _ := parquetcol.NewWriter(&buf, schema, nil)
_ = w.WriteRow(parquetcol.Struct(map[string]parquetcol.Value{
    "id":   parquetcol.Int64(42),
    "tags": parquetcol.List([]parquetcol.Value{parquetcol.String("a")}),
    "meta": parquetcol.Struct(map[string]parquetcol.Value{}), // 空结构
}))
_ = w.Close()

r, _ := parquetcol.NewReaderFromBytes(buf.Bytes())
rows, _ := r.ReadAll()
```

## 设计约定与边界

- 记录是 `map[string]Value`；缺失的可选字段与显式 `Null()` 在 Parquet 中不可区分，
  `Value.Equal` 将二者视为相等。
- 裸的顶层 `repeated` 字段无法区分 null 列表与空列表（Parquet 格式本身的限制）；
  需要区分时使用 `ListOf`（带 optional 外层包装）。
- 仅支持未压缩列（`UNCOMPRESSED`）；不写 DataPageV2、索引页与逻辑类型联合的新字段。
- 空文件（零行）不会产生行组；footer 仍合法。
