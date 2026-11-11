# sigv4chunk

Go 网关的 SigV4 `aws-chunked` 流式上传验签中间件。每个块的签名都依赖
前一块的签名形成签名链，任何一块验证失败，下游处理器都不会被调用。

## 验证流程

1. 要求 `x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD`，解析
   `Authorization` 头中的 Credential、SignedHeaders、Signature。
2. 构造规范请求：路径按段做严格百分号编码（仅 RFC 3986 unreserved 字符
   保持原样、`/` 分段保留、不做 `.`/`..` 归一化）；查询参数先解码再按
   `(key, value)` 码点排序后重新编码；头部名小写、值折叠空白并按名排序。
3. 校验 `X-Amz-Date`（格式、与 credential scope 日期一致、时钟偏移窗口），
   并要求 `host`、`x-amz-date`、`x-amz-content-sha256`、
   `x-amz-decoded-content-length` 都在 SignedHeaders 中。
4. 派生签名密钥并校验种子签名（常量时间比较）。
5. 逐块解析 `chunk-size;chunk-signature=<hex>\r\n<data>\r\n`，用
   `AWS4-HMAC-SHA256-PAYLOAD` 串接前一块签名验签；最后必须有签名的空块，
   空块之后出现任何尾随字节一律拒绝。
6. 实际解码字节数必须等于 `X-Amz-Decoded-Content-Length`；全部验证通过后，
   请求体才被替换为纯明文（修正 `ContentLength`、剥离分帧头）交给处理器。

`chunkVerifier` 本身是 fail-closed 的流式 reader：一旦某块失败，后续所有
`Read` 都返回同一个错误，绝不吐出未验证数据。中间件在调用处理器前会先
完整验证并缓冲整个请求体，因此处理器只能看到已验证数据。

## 用法

```go
handler := sigv4chunk.Middleware(sigv4chunk.Config{
    Keys: func(accessKey string) (string, bool, error) {
        secret, ok := keyStore[accessKey]
        return secret, ok, nil
    },
    AllowedRegion:  "us-east-1",
    AllowedService: "s3",
    MaxSkew:        15 * time.Minute,
}, uploadHandler)
http.ListenAndServe(":8080", handler)
```

## 测试

- `TestAWSGoldenGetVanilla` 使用 AWS 公开 SigV4 测试向量（IAM ListUsers，
  2015-08-30，`AKIDEXAMPLE`）逐字节断言规范请求与种子签名。
- 独立 Ruby/OpenSSL HMAC 实现交叉核对过种子与块签名链（见 `crosscheck.rb`，
  `ruby --disable-gems crosscheck.rb`）。
- 覆盖块数据篡改、块签名/种子签名伪造、截断（块中与缺失最终空块）、
  块重排、声明长度偏大/偏小、尾随垃圾、过期请求、未知 Access Key、
  缺少必签头等场景，全部断言返回非 2xx 且处理器不被调用。

```sh
go test -race ./...
```
