# URI约束只看主机

URI约束把路径也算进去，攻击者换一条路径就绕过。只比较主机部分，主机按DNS约束的规则；路径、查询和用户信息都不参与。主机匹配而路径不同的URI放行。可导入的Go库供证书路径调用。保持主机，改掉路径和用户信息。

## 行为

- URI约束只提取并比较主机；路径、查询、片段、用户信息和端口都不参与匹配。
- 主机按 DNS name constraint 规则比较：忽略大小写，约束匹配该主机及其子域。
- 以 `.` 开头的约束要求至少存在一个子域标签；`*` 不具特殊通配含义。
- 没有主机、主机是 IP 地址、或主机格式非法的 URI 会被拒绝。
- 约束本身也可以是完整 URI，但同样只使用其主机部分。

## 用法

```go
matched, err := netsec.MatchURIConstraint(uri, "example.com")
if err != nil {
    // invalid URI host or constraint
}

constraints, err := netsec.NewURIConstraints(
    []string{"example.com"},
    []string{".blocked.example.com"},
)
if err != nil {
    // invalid constraint
}
if err := constraints.Check(uri); err != nil {
    // URI is excluded or not permitted
}
```

`CheckCertificateURIConstraints` 可直接用于遍历证书中的 URI SAN。
