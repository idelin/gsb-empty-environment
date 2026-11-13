# PDF增量未覆盖

增量更新改了正文，签名的字节范围却停在上一版。签名的字节范围必须覆盖除签名值以外的当前全部字节；有洞则失败。范围覆盖当前版本的签名接受。可导入的Go库供文档解析调用。在签名范围之后追加一个对象。

## 用法

```go
doc, err := netsec.Parse(pdfBytes)
if err != nil {
    // 词法或对象结构错误
}

// 只要有一个签名的 /ByteRange 覆盖当前完整文件（仅排除其 /Contents），
// 文档即通过。字节范围在旧版本结束、尾部有新字节、或中间出现洞时失败。
err = doc.Validate()
```

底层检查可直接由其他 PDF 解析器调用：

```go
err := netsec.ValidateByteRange(fileSize, byteRange, contentsStart, contentsEnd)
```

`byteRange` 为 PDF 中 `/ByteRange` 的四个整数。`contentsStart`/`contentsEnd`
是 `/Contents` 字符串 token 的半开区间；省略的字节必须全部落在这个区间内。

在签名范围之后追加对象：

```go
updated, err := netsec.AppendObject(pdfBytes, []byte("4 0 obj\n<< /Type /Metadata >>\nendobj\n"))
```

追加不会改动已签名的字节。若 `/ByteRange` 未同步扩展到新的文件末尾，
随后调用 `Validate` 会以 `ErrUncoveredBytes` 失败。

## 结果

- `doc.Signatures`：扫描到的全部签名，包括早期增量版本中的签名。
- `doc.Results()`：逐签名结果，可用于区分旧签名和当前签名。
- `doc.CurrentSignatures()`：覆盖当前版本的签名。
- `ValidateByteRange`：不依赖本包解析器的字节范围检查。
