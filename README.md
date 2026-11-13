# 发行者与状态列表来源

状态列表的 `uri` 必须与凭证 `issuer` 的主机一致，否则不采信该列表，也不能用
它判断吊销。同主机的状态列表可以参与判断。本模块是可被验证组件导入的 Go 库。

## 安全规则

- 只比较规范化后的主机名。协议、端口、路径、查询参数和片段不属于主机。
- 主机比较不区分大小写，并会规范化末尾点号与 IPv6 地址。
- 相对 URI、缺少主机的 URI、畸形 URI 一律不采信。
- 不同主机的列表在发起任何网络请求前就被拒绝。
- 同一主机的 HTTP 跳转仍会被限制在原主机，避免通过跳转绕过主机绑定。

## 使用

```go
import "example.org/netsec/q0178"

if err := netsec.ValidateStatusListURI(issuer, statusListURI); err != nil {
    // 列表来源不可信，不能用于吊销判断
    return err
}
```

完整的吊销检查会先校验来源，再抓取并解析 W3C Bitstring Status List：

```go
v := &netsec.Verifier{Fetcher: &netsec.HTTPFetcher{}}
result, err := v.CheckRevocation(ctx, issuer, netsec.StatusListReference{
    URI:   statusListURI,
    Index: index,
})
if err != nil {
    // 无法得到可信状态，按校验失败处理
    return err
}
if result.Revoked {
    // 凭证已吊销
}
```

`StatusListFetcher` 可替换为自定义抓取器（例如带签名的离线缓存），但验证组件
仍应在使用列表前调用 `ValidateStatusListURI`。
