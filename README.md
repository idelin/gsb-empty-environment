# nameconstraint

内部CA签发的中间证书带名称约束，旧校验只看叶子SAN，攻击者拿受限中间CA给外部域名签了证书。给Go的TLS接入层实现可挂到VerifyPeerCertificate的链校验包：对DNS、IP和邮箱按允许与排除子树逐级求交，约束作用于链上所有下级证书，通配符SAN和尾点按规范处理，无SAN时不回退CN。测试排除优先、空允许集、IP掩码边界和交叉签名的多条路径。

## 用法

```go
import "example.org/nameconstraint"

tlsConfig := &tls.Config{
    VerifyPeerCertificate: nameconstraint.VerifyPeerCertificate,
}
```

也可以在已构建的链上直接调用 `nameconstraint.VerifyChain(chain)`，链按叶子在前、信任锚在尾排列。

## 语义

- DNS、IP、邮箱三类名字各自独立执行允许/排除子树匹配；排除子树优先于允许子树。
- 约束来自链上每一级 CA（含信任锚），作用于其所有下级证书的 SAN，而不仅是叶子。
- DNS 约束无前导点时匹配本域及子域，有前导点（`.example.com`）只匹配子域；大小写不敏感，尾点（absolute name）先规范化。
- 通配符仅接受最左完整标签为 `*` 的 SAN，剥掉 `*` 标签后按普通域名匹配；`a*.x` 等形态按字面量处理。
- 邮箱约束含 `@` 时为精确信箱，否则按域名约束匹配邮箱域部分；本地部分大小写敏感。
- IP 约束按 CIDR 掩码逐位求交，IPv4/IPv6 长度不匹配即不命中，拒绝非连续掩码。
- 证书没有 SAN 扩展时不做任何名称匹配，绝不回退到 Common Name。
- 显式存在但为空的 permittedSubtrees 会拒绝所有受约束名字类型；本包自带宽松 DER
  解析，可处理标准库 `crypto/x509` 拒绝的该类证书。
- 交叉签名场景下，`VerifyPeerCertificate` 对所有已验证链逐一检查，任一条链满足即通过。

## 测试

`go test ./...` 覆盖：外部域名攻击复现、排除优先、空允许集、IP 掩码边界（含 IPv6
与族不匹配）、邮箱精确/域匹配、嵌套约束、交叉签名多路径、无 SAN 不回退 CN、
通配符与尾点规范化以及错误归因。
