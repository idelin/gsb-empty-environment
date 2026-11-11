# 邮件正文局部签名可见性

邮件安全界面需要标出正文哪些可见字符未被签名覆盖，原始字节长度不能直接映射到解码后文本。写供Go预览宿主调用的映射包，输入MIME叶节点原始字节区间、quoted-printable或Base64编码及已认证原始区间集合；每个解码字节记录依赖的全部原始字符，只有依赖全部被认证才标可信，跨编码单元的半覆盖标不可信。用软换行、填充、截断和跨片段覆盖验证高亮不能夸大认证范围。

## 用法

包 `netsec` 的入口是 `MapPart(raw, encoding, authenticated)`：

- `raw`：MIME 叶节点的原始字节（Content-Transfer-Encoding 解码前）。
- `encoding`：`EncodingIdentity`（7bit/8bit/binary）、`EncodingQuotedPrintable` 或 `EncodingBase64`。
- `authenticated`：签名实际覆盖的原始字节半开区间 `[Start, End)`，可乱序、相邻或重叠，越界/反向区间返回错误。

返回的 `Mapping` 含：

- `Decoded`：解码后的正文字节，宿主可直接渲染；
- `Bytes`：与 `Decoded` 逐字节对应的 `ByteTrust`，其中 `Deps` 是产生该字节所依赖的全部原始字符位置，`Trusted` 仅当这些位置全部被认证时为 true；
- `Issues`：截断（`IssueTruncated`）与损坏（`IssueMalformed`）记录，相关字节一律不可信；
- `RuneRanges()`：把逐字节结果聚合成界面高亮区间，跨认证边界的多字节 rune 整体降级为不可信，不会切开可见字符。

## 安全语义（高亮不得夸大认证范围）

- **QP 软换行** `=CRLF` / `=LF`：纯传输框架，不产生解码字节，`=` 与换行位置不进入任何依赖；只认证框架字符不会让任何正文变可信。
- **QP 转义** `=XX`：一个解码字节依赖三个原始字符，任一未认证即不可信。
- **Base64 量子**：以完整 4 字符量子为依赖单元（含填充 `=`），量子内任一字符未认证，则其 1~3 个解码字节全部不可信。
- **Base64 截断**：结尾不完整量子尽力输出但强制不可信并记录 `IssueTruncated`，即使已有符号全部已认证。
- **CR/LF 折行**：不属于 Base64 量子依赖；空格、制表符等非法字符按损坏处理并停止解码。
- **跨片段覆盖**：一个编码单元的依赖可由多个相邻/交叠的已认证区间拼满，拼满才可信；接缝切开单元则整体不可信。

## 验证

`go test ./...` 覆盖软换行、填充、截断、跨片段覆盖、半覆盖降级、UTF-8 rune 边界，并对合法输入与标准库 `mime/quotedprintable`、`encoding/base64` 做逐字节对拍。
