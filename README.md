# IPFS网关屏蔽名单规范化

攻击者用同一内容的CIDv0、CIDv1和不同multibase写法，绕过IPFS网关按multihash登记的屏蔽名单。给Go网关写供请求路由调用的授权包：路径里的CID先规范化到multihash再查名单，/ipns/名称解析后的目标同样检查；路径网关命中可执行内容类型时重定向到子域，Host不在调用方域名下拒绝。用v0转v1、大小写变形和解析后命中验证。

## 实现

`netsec` 是只依赖 Go 标准库的授权包，屏蔽名单以 multihash 为键，因此
CIDv0、CIDv1 以及同一 CIDv1 的任意 multibase 写法都会归约到同一个
multihash 后再判定。

- `multihash.go`：自描述 multihash（varint code + varint length + digest）
  的构造、校验与规范化。
- `multibase.go`：base16/base32/base36/base58btc/base64(+url) 编解码，
  大小写不敏感的编码统一解码为相同字节。
- `cid.go`：`ParseCID` 同时接受 CIDv0（裸 base58btc）与带 multibase 前缀
  的 CIDv1；`V1()` 做 v0→v1 转换并保持 dag-pb codec 与 multihash 不变；
  `DNSLabel()` 生成子域用的 base32 标签。
- `blocklist.go`：并发安全、按 multihash 去重的屏蔽名单。
- `authorizer.go`：请求路由调用入口。

## 路由接入

```go
auth := netsec.New(netsec.Config{
    Blocklist:     blocklist,                 // 按 multihash 登记
    Resolver:      netsec.ResolverFunc(myIPNS),
    AllowedHosts:  []string{"gw.example.net", "ipfs.example.net"},
    SubdomainHost: "ipfs.example.net",
})

func handler(w http.ResponseWriter, r *http.Request) {
    // 1. Host 校验 + CID 规范化查名单（/ipns/ 会解析后再查）。
    decision := auth.Authorize(r)
    if decision.Action == netsec.ActionDeny {
        http.Error(w, decision.Reason.Error(), decision.Status)
        return
    }

    // 2. 可执行内容类型在路径网关下重定向到按 CID 隔离的子域。
    if policy := auth.ContentPolicy(r, responseContentType, decision); policy.Action == netsec.ActionRedirect {
        http.Redirect(w, r, policy.Location, policy.Status)
        return
    }
    // ...正常放行
}
```

## 安全语义

- 名单键是 multihash，与 CID 文本形态无关；v0 与 v1、大小写/混合大小写
  base32、base36、base16、base58btc、base64 等写法全部命中同一条记录。
- `/ipns/` 名称经 `Resolver` 解析，支持多层 `/ipns/` 链路（上限 32 跳，
  带环检测）；解析失败、循环或目标非法一律 fail-closed 拒绝，绝不放行。
- `Host` 不在 `AllowedHosts`（整标签、大小写不敏感、剥离端口）内返回
  403；子域信任域不隐式包含父域之外的名字。
- `text/html`、`application/xhtml+xml`、`image/svg+xml`、`application/pdf`
  等可执行类型在路径网关下 302 到 `<cidv1-base32>.ipfs.<host>`；未配置
  子域宿主时拒绝而非在共享源下发活动内容。

## 验证

```sh
go test -race -cover ./...
```

测试覆盖 v0→v1 同 multihash、base32/base36/base16 等大小写与混合大小写
变形归一、IPNS 直连/链式解析命中与 fail-closed、外部 Host 拒绝、可执行
类型子域重定向等场景。
