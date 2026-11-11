# derstrict

严格 DER（ITU-T X.690）解码包。`encoding/asn1` 会接受若干 BER 风格的非规范
编码，导致同一张证书存在两种合法字节表示、签名缓存失效；本包只接受唯一的
规范 DER 表示，并报告**首个违规字节的偏移**。

## 拒绝的非规范编码

- 非最短长度：长形式前导 `0x00`，或短值（≤127）使用长形式
- 不定长编码（`0x80`，BER 专用）
- 非规范布尔值：内容只允许 `0x00` / `0xff`
- 整数多余前导字节：冗余 `0x00`（正数）或 `0xff`（负数）
- 未排序的 `SET OF`：成员完整 TLV 必须按字典序升序（X.690 11.6）
- 尾随数据：顶层元素之后的任何字节（嵌套内容中的残片同样拒绝）
- 额外检查：非最短高标签号、空 INTEGER、非空 NULL、`BIT STRING` 未用位、
  通用类型的构造/原语位错误、截断输入

## 用法

```go
node, err := derstrict.Decode(certDER)
if err != nil {
    offset := derstrict.OffsetOf(err) // 首个违规字节偏移，非本包错误返回 -1
}
```

按调用方给的模式解码嵌套结构（模式之外仍强制执行全部 DER 规则）：

```go
pattern := derstrict.Pattern{
    Class: derstrict.ClassUniversal, Tag: derstrict.TagSequence,
    Children: []derstrict.Pattern{
        {Class: derstrict.ClassUniversal, Tag: derstrict.TagSequence},
        {Class: derstrict.ClassUniversal, Tag: derstrict.TagSequence},
        {Class: derstrict.ClassUniversal, Tag: derstrict.TagBitString},
    },
}
node, err := derstrict.DecodeWithPattern(certDER, pattern)
```

`Pattern` 支持 `AnyTag` / `AnyClass` 通配和末位 `Repeat`（匹配零个或多个同类
子元素）。`Node` 暴露 `Class`、`Tag`、`Constructed`、`Offset`、`Content`、
`Full`、`Children`，并提供 `Bool()` 与 `Integer()` 访问器。

## 与 encoding/asn1 的接受范围对比

测试中逐项固化（见 `comparison_test.go`）：

| 变形样本 | derstrict | encoding/asn1 |
| --- | --- | --- |
| 非最短长度 | 拒绝 | 接受（残片留在 `rest`） |
| 不定长编码 | 拒绝 | 拒绝（语法错误） |
| 非规范布尔值 | 拒绝 | 接受（非零即 true） |
| 整数多余前导字节 | 拒绝 | 接受 |
| 未排序 SET OF | 拒绝 | 接受（无排序检查） |
| 尾随数据 | 拒绝 | 接受（通过 `rest` 返回） |
| 非最短高标签号 | 拒绝 | 拒绝 |

## 测试

- `derstrict_test.go`：合法嵌套结构、整数正负值、SET 排序、高标签号
- `mutation_test.go`：逐项变形样本，断言违规操作类型与字节偏移
- `comparison_test.go`：与 `encoding/asn1` 的逐行接受范围矩阵
- `pattern_test.go`：模式匹配、通配、`Repeat`、违规偏移
- `cert_test.go`：`crypto/x509` 生成的真实证书、证书内布尔与长度变形

```sh
go test ./...
```
