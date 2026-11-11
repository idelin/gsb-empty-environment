# signedbody

网关转发带签名的分块请求时，业务处理器有时会先收到半份未验签正文。给Go的net/http服务实现请求中间件，按“上块摘要、块序号、正文”验证HMAC-SHA256链，末块签名绑定总长；完整验签前禁止调用下游。超过内存预算时落入请求专属临时文件，用真实HTTP请求测试并发取消、合法空正文和坏末块后的文件回收。

## 线格式

请求体为分帧序列 `[4字节大端长度][块负载]`，以 EOF 结束。链式摘要：

```
d_0 = HMAC(key, "signedbody/chunk/v1" ‖ 0^32 ‖ seq ‖ len ‖ payload)
d_i = HMAC(key, "signedbody/chunk/v1" ‖ d_{i-1} ‖ seq ‖ len ‖ payload)
sig = HMAC(key, "signedbody/final/v1" ‖ d_last ‖ totalLen)
```

末块签名（hex）经 `X-Signedbody-Signature` 头发送，绑定的总长经
`X-Signedbody-Total-Length` 头发送。空正文（零块）合法，签名对
`0^32 ‖ 0` 计算。

## 用法

```go
handler := signedbody.Middleware(signedbody.Config{
    Key:          hmacKey,       // 共享HMAC密钥
    MemoryBudget: 1 << 20,       // 超出后落入请求专属临时文件
    TempDir:      "/var/tmp/app", // 缺省 os.TempDir()
}, downstream)
http.ListenAndServe(":8080", handler)
```

中间件完整验签通过后才会调用下游，并把 `r.Body` 替换为已验签明文
（内存或临时文件）、恢复 `r.ContentLength`；任何失败（截断、取消、
链不匹配、总长不符）都返回 401 且绝不触达下游。请求结束时临时文件
一律回收。发送方用 `signedbody.EncodeBody` 分帧、`signedbody.Sign`
计算签名头。

## 测试

```sh
go test -race ./...
```

测试通过内存监听器跑完整的 net/http 服务端与客户端协议栈（真实
HTTP/1.1 报文），覆盖并发取消、合法空正文、坏末块拒绝及临时文件回收。
