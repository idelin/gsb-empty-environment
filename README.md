# ctxescape

评论渲染把用户文本拼进HTML正文、属性、URL和内联脚本，按单一规则转义会留下XSS。给Go渲染服务实现上下文感知的转义写入器：增量跟踪HTML解析状态，模板片段可在任意字节处切开，按当前上下文选择转义方式，URL属性拒绝javascript与data方案，无法判定上下文时拒绝输出而不是猜测。测试无引号属性、注释与CDATA、脚本内字符串和跨块切分。

## 设计

`Writer` 是一个 `io.WriteCloser`，区分两类输入：

- `Write` / `WriteString`：受信任的模板字节，驱动一个增量 HTML 状态机并原样转发。
- `WriteText`：不可信文本，按当前解析上下文选择转义规则后写出，且不改变解析状态。

模板可以在任意字节处切分成多个 chunk（包括标签、注释、多字节 rune 中间），
状态机用一个 `pending` 缓冲区保留“可能是更长记号前缀”的尾部，其余字节立即刷出。

## 上下文与策略

| 上下文 | 处理 |
| --- | --- |
| HTML 正文、`<textarea>`/`<title>` (RCDATA) | 转义 `& < > " ' +`，NUL 与非法 UTF-8 替换为 U+FFFD |
| 单/双引号属性 | 同上，引号以实体编码 |
| 无引号属性 | 保守安全集之外的字节全部写成十六进制实体，空白、`` ` ``、`=` 等无法跳出 |
| URL 属性 (`href`/`src`/...) | 先做方案白名单校验，再按属性上下文转义 |
| `<script>` 内字符串/模板串 | JS 字符串转义，HTML 特殊字符用 `\uXXXX`，模板串额外中和 `` $ { } `` |
| 事件处理属性 | JS 转义（无引号时再叠加 HTML no-space 转义） |
| HTML 注释、CDATA、`<style>`、JS 语句位、标签/属性名中、声明 | 拒绝插值 |

URL 仅允许相对地址和 `http`、`https`、`mailto` 方案；`javascript:`、`data:`、
`vbscript:` 等（含大小写混淆与前导空白）一律返回 `ErrUnsafeURL`。

## 失败即拒

- `ErrAmbiguousContext`：当前上下文无法安全建模，或上一 chunk 停在未完成的记号里。
- `ErrUnsafeURL`：URL 属性值使用了非白名单方案。
- `ErrIncompleteStream`：`Close` 时流仍停在标签、注释、script 等未闭合状态。

出错后 Writer 进入中毒状态，后续调用返回同一错误，绝不会把半成品写到下游。

## 用法

```go
var buf bytes.Buffer
w := ctxescape.NewWriter(&buf)
w.WriteString(`<a href="`)
if err := w.WriteText(userURL); err != nil {
    // 例如 javascript: 被拒绝
    return err
}
w.WriteString(`" title="`)
w.WriteText(userComment) // 按引号属性上下文转义
w.WriteString(`">`)
if err := w.Close(); err != nil {
    return err
}
```

## 测试

- 无引号/单双引号属性的 breakout 向量
- 注释与 CDATA 的插值拒绝，以及跨 chunk 的记号识别
- script 内普通字符串、模板字符串、事件属性、语句位拒绝
- 大小写混合与带分隔符的 `</script >` 结束标签
- 任意字节切分与整块写入的输出等价性（含两个 fuzz target）
- 多字节 rune、非法 UTF-8、DOCTYPE、URL 方案混淆
