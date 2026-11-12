# CAA issuewild查找冲突

签发服务查询CAA时，通配名、父域层级和CNAME会改变授权结果，攻击者可借子域记录绕开上级限制。为Go签发前检查包实现RFC 8659查找，供订单授权调用：沿CNAME向上取首个非空记录集，通配名优先issuewild并回退issue，空issue值禁止签发，未识别的critical标记拒绝。用多层域名、CNAME链、issuewild与issue冲突验证。

## 使用

`Resolver` 提供按 DNS 所有者查询 CAA 与 CNAME 的底层能力，检查器负责
RFC 8659 的 CNAME 追踪和父域爬升：

```go
checker := netsec.Checker{
	Resolver: dnsResolver,
	Issuer:   "ca.example.com",
}
if err := checker.Authorize(ctx, "shop.example.com", false); err != nil {
	// 拒绝签发，错误可通过 errors.Is 与 netsec.Err* 比较。
}
```

通配证书传入去掉 `*.` 的名称并设置 `wildcard=true`，或直接传入
`*.shop.example.com`。查找只爬升原始请求名的父域；CNAME 目标没有 CAA 时，
继续检查原始名的父域，这与 RFC 8659 取代 RFC 6844 后的简化算法一致。
