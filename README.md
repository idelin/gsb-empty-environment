# Trailer 字段认证与合并

`netsec` 供消息认证网关使用：发送方在头部预声明允许出现的尾部字段，并用摘要回调生成覆盖头部区和尾部区的签名；接收方只有在字段策略和签名都通过后，才把尾部字段合并进头字段。任何失败都会返回 `nil`，不会产生部分合并结果，下游不会取得未认证消息。

## 安全规则

- 尾部字段必须由头部 `Trailer` 字段预声明。
- `Authorization`、`Proxy-Authorization`、`Cookie`、`Signature` 等认证/凭据字段禁止出现在尾部。
- `Host`、`Forwarded`、`X-Forwarded-*`、`Via` 等路由字段禁止出现在尾部。
- `Content-Length`、`Transfer-Encoding`、`Trailer`、`Connection` 等长度/分帧字段禁止出现在尾部。
- 头尾同名字段一律拒绝，即使值相同，也不会覆盖已鉴权头字段。
- 声明了但未收到的尾部字段视为截断，直接拒绝。
- `Digest`、`Content-Digest`、`Repr-Digest` 等摘要字段允许放在尾部。
- 签名输入使用带长度前缀的规范编码，绑定头部区、尾部区、各自的字段顺序以及重复实例。

## 基本用法

发送方：

```go
header := http.Header{"Content-Type": {"application/json"}}
if err := netsec.DeclareTrailers(header, "Digest"); err != nil {
	log.Fatal(err)
}
trailers := http.Header{"Digest": {"sha-256=:..."}}

tag, err := netsec.SignHTTP(header, trailers, hmacSHA256(key))
if err != nil {
	log.Fatal(err)
}
header.Set(netsec.SignatureHeader, netsec.EncodeSignature(tag))
```

接收方：

```go
declared := netsec.CaptureDeclaredTrailers(r.Trailer) // 必须在读 body 之前
body, err := io.ReadAll(r.Body) // 必须先读到 EOF，尾部字段才会完整
if err != nil {
	log.Fatal(err)
}
merged, err := netsec.MergeSignedTrailersWithDeclaration(r.Header, r.Trailer, declared, hmacSHA256(key))
if err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
// 只有走到这里，merged 中的 Digest 才是已认证的尾部字段。
```

## net/http 注意事项

Go 的 `net/http` 在解析请求或响应后会从 `Header` 中删除 `Trailer` 字段，并把它转换成 `Trailer` map。推荐在读取 body 之前调用 `CaptureDeclaredTrailers(r.Trailer)` 保存预声明集合，再调用 `MergeSignedTrailersWithDeclaration`；这样即使 `net/http` 之后把未声明字段混入 map，也会按原始预声明直接拒绝。`MergeTrailers` 要求头部仍带显式声明；`SignHTTP`/`VerifyHTTP` 会把声明规范化，签名输入绑定规范化后的字段名集合。

如果需要绑定不同字段名之间的真实到达顺序，请使用 `Field`/`Section` API（`Sign`、`Verify`、`MergeSections`）。`http.Header` 是 map，无法保留跨字段名的原始顺序；`SignHTTP` 会按字段名排序，但保留同一字段的重复值顺序。

## 主要 API

- `DeclareTrailers` / `DeclaredTrailers`：写入和解析头部预声明。
- `ValidateTrailers` / `ValidateSections`：检查声明、禁用字段、头尾冲突和截断；未预声明直接拒绝。
- `ValidateParsedTrailers`：兼容 `net/http` 已删除 `Trailer` 字段的场景，从尾部 map 重建声明。
- `CaptureDeclaredTrailers`：在读取 body 前保存 `net/http` 的预声明集合。
- `Sign` / `Verify` / `SigningInput`：对有序 `Section` 签名和验签。
- `SignHTTP` / `VerifyHTTP`：`http.Header` 便捷封装。
- `MergeSignedTrailers` / `MergeSignedTrailersWithDeclaration`：从 `X-Message-Signature` 读取签名并完成完整合并。
- `MergeTrailers` / `MergeTrailersWithDeclaration` / `MergeSections`：验签并返回合并结果；失败返回 `nil`。

## 测试

```sh
go test ./...
```

测试使用真实的 HMAC-SHA-256 摘要回调，覆盖头尾同名、未声明尾字段、截断、篡改、字段顺序和重复实例，并通过 `net/http` 的 chunked 尾部解析验证下游不会收到未认证字段。
