# anyPolicy不能被映射

策略映射（`policyMappings`）不能把 `anyPolicy`（`2.5.29.32.0`）映射成别的 OID。
`issuerDomainPolicy` 和 `subjectDomainPolicy` 两端都不能是 `anyPolicy`；任一端出现
`anyPolicy` 时验证失败。两个具体 OID 之间的合法映射会原样保留。

本包是可直接导入的 Go 库，可用于证书路径验证。主要 API：

- `ValidatePolicyMappings(mappings)`：接受 `[]PolicyMapping`、`[]x509.PolicyMapping`、
  证书或等价的映射结构，发现任一端为 `anyPolicy` 时返回 `ErrAnyPolicyMapping`。
- `NormalizePolicyMappings(mappings)`：验证并保留所有具体 OID 之间的映射。
- `ParsePolicyMappingsExtension(der)`：解析并验证 `policyMappings` 扩展 DER。
- `ValidateCertificatePolicyMappings(cert)`：验证单张证书，可直接作为路径验证钩子。
- `ValidatePathPolicyMappings(chain)`：验证整条证书链中的映射。

```go
import (
	"errors"

	netsec "example.org/netsec/q0273"
)

err := netsec.ValidatePolicyMappings(netsec.PolicyMapping{
	IssuerDomainPolicy:  netsec.OID{2, 5, 29, 32, 0}, // anyPolicy: 不允许
	SubjectDomainPolicy: netsec.OID{1, 2, 3, 4},
})
if errors.Is(err, netsec.ErrAnyPolicyMapping) {
	// 拒绝该证书路径
}
```
