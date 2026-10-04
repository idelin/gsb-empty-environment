# configcomplete

面向类 JSON 配置语言（嵌套对象、数组、转义字符串）的上下文补全库。
给定不完整文本与光标位置，返回光标处合法的 token 类别，以及把文本
补成完整合法文档所需的最短后缀。

## 语言定义

```
document := value
value    := object | array | string | number | true | false | null
object   := "{" [ string ":" value { "," string ":" value } ] "}"
array    := "[" [ value { "," value } ] "]"
string   := '"' { char | "\" ('"' '\' '/' 'b' 'f' 'n' 'r' 't')
                 | "\u" hex hex hex hex } '"'
number   := [-] ( "0" | [1-9] [0-9]* ) [ "." [0-9]+ ] [ ("e"|"E") ["+"|"-"] [0-9]+ ]
```

token 之间允许空白（空格、tab、CR、LF）。字符串中不允许未转义的
控制字符；>= 0x80 的字节（如 UTF-8 多字节字符）原样接受。

## API

```go
res := configcomplete.CompleteAt(text, cursor) // cursor 为字节偏移
// 或
res := configcomplete.Complete(prefix)         // 只分析光标前文本
ok  := configcomplete.Valid(doc)               // 完整文档校验
```

`Result` 字段：

- `Conflict *Conflict`：非空表示光标前文本已无法扩展成合法文档，
  `Pos` 为**最早冲突**的字节偏移（第一个使任何扩展都失败的位置）。
- `Categories []TokenCategory`：光标处合法的 token 类别；文档已完整
  时为空。光标在字符串内部时返回串内类别（`CatStringChar` /
  `CatEscape` / `CatEscapeHex` / `CatStringEnd`）。
- `Suffix string`：追加在光标处即可得到完整合法文档的最短字节串。

## 语义约定

- **前缀模型**：`CompleteAt` 只分析 `text[:cursor]`，忽略光标之后
  的内容。
- **不修改有效 token**：补全只追加，不改写光标前已成立的 token。
  光标恰好落在未完成的 token（未闭合字符串、`-`、`1.`、`tru` 等）
  上时，会把该 token 补完；而 `[1` 中 `1` 已是合法完整 token，因此
  不会提供把它改成 `12` 或 `1.5` 的补全。
- **字符串内按原字节定位**：所有位置都是原始文本的字节偏移，`\n`
  的两个字节是两个不同位置（`\` 后与 `\u` 的十六进制位中都有各自
  的补全上下文）。
- **最早冲突**：词法错误（非法字符、坏转义、串内控制字符）与语法
  错误（如 `{"a" 1}` 缺冒号）都定位到第一个无法扩展的字节。

## 实现与验证

补全由真实的词法器 + 显式解析状态机驱动，不靠关键词表硬猜上下文：
词法器产出完整 token、末尾的“部分 token”或最早冲突；状态机给出
合法类别；最短后缀由状态机继续状态上的递推（`minComplete`）算出，
其中逗号状态取“直接闭合”这一不动点最小解。

测试采用**生成完整样例再截断**的核对方式（`truncate_test.go`）：
随机生成合法文档（含嵌套、各类转义、多字节字符、多种数字形式），
对每个字节截断点断言：

1. 不报冲突（原文剩余部分就是可扩展性的见证）；
2. 原文中实际跟随截点的 token 类别 ∈ 返回类别（数字被截成合法前缀
   时按“不修改有效 token”规则豁免）；
3. `prefix + Suffix` 是完整合法文档，且截点前 token 序列保持不变；
4. `len(Suffix) <= len(原文剩余部分)`（最短性 sanity 上界）。

另有表驱动的精确断言（`complete_test.go`）覆盖各类上下文的最短
后缀与冲突位置。

## 运行测试

```sh
go test ./...
```
