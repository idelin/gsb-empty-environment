# DNS约束不含前导点

约束写成.example.com时被解释成只限制子域，example.com本身漏掉。带前导点的DNS约束直接无效；不带点的约束同时匹配该名和其子域。约束example.com能匹配a.example.com和example.com。可导入的Go库供证书路径调用。用带点的约束去匹配example.com本身。

## 用法

```go
import "example.org/netsec/q0253"

matched, err := netsec.MatchDNSNameConstraint("example.com", ".example.com")
// matched == true, err == nil

err = netsec.CheckDNSNameConstraints(
	"a.example.com",
	[]string{".example.com"},
	nil,
)
// err == nil
```

`MatchDomainConstraint` 与 `MatchDNSNameConstraint` 等价；
`CheckCertificateNameConstraints` 可直接用于 `*x509.Certificate` 的
`PermittedDNSDomains` / `ExcludedDNSDomains`。
