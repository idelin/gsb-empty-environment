// Package samlsec 提供仅依赖 Go 标准库的 SAML 2.0 Response/Assertion 安全验证。
//
// 设计目标是同时抵御三类攻击：
//   - XML 外部实体（XXE）与 DTD 炸弹：解析前对原始字节流做硬扫描，任何
//     <!DOCTYPE / <!ENTITY 或其他 DTD 声明一律拒绝。
//   - XML 签名包装（XSW1-XSW8）：不允许通过 Reference URI 自由定位任意节点，
//     被签名元素的完整树路径被强制限制为根 Response 或其唯一直接子 Assertion，
//     且业务层身份数据只能从验签通过的同一节点子树读取。
//   - 注释 / 处理指令截断：NameID、Issuer、Audience 等安全敏感元素内部出现
//     任何注释或处理指令直接判定为非法，杜绝 admin<!-- -->@evil.com 类冒充。
//
// 本包不引入任何第三方 XML 签名库，Exc-C14N、摘要与非对称验签全部基于
// encoding/xml、crypto/rsa、crypto/ecdsa 与 crypto/sha256 实现。
package samlsec

// SAML 与 XML-DSig 命名空间常量。
const (
	NSSAML  = "urn:oasis:names:tc:SAML:2.0:assertion"
	NSSAMLP = "urn:oasis:names:tc:SAML:2.0:protocol"
	NSDSIG  = "http://www.w3.org/2000/09/xmldsig#"
	NSXML   = "http://www.w3.org/XML/1998/namespace"
)

// 支持的算法标识。算法集合刻意保持收敛：仅接受 SHA-256 摘要、Exc-C14N
// 规范化以及 SHA-256 系列非对称签名，SHA-1 / Inclusive C14N 等历史弱算法
// 一律按 ErrUnsupportedAlgorithm 拒绝。
const (
	AlgoSHA256       = "http://www.w3.org/2001/04/xmlenc#sha256"
	AlgoExcC14N      = "http://www.w3.org/2001/10/xml-exc-c14n#"
	AlgoEnveloped    = "http://www.w3.org/2000/09/xmldsig#enveloped-signature"
	AlgoRSASHA256    = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	AlgoRSAPSSSHA256 = "http://www.w3.org/2001/10/xmldsig-more#rsa-sha256"
	AlgoECDSASHA256  = "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256"
)
