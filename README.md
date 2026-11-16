# SAML NameID 注释截断防护

攻击者在 IdP 注册了形如 admin@victim.com.evil.com 的名字并在其中插入注释，签名有效，而有的解析器只取首个文本节点，读成 admin@victim.com。为Go SAML 消费端写可导入的 NameID 读取包：签名摘要按排除注释的规范化计算，注释可被改动而摘要不变；NameID 内含注释、CDATA 或多个文本子节点时一律拒绝，否则取完整文本；首尾空白拒绝。用注释分割、CDATA、纯文本和注释在元素之外验证。

## 用法

```go
import (
	"crypto/sha256"

	netsec "example.org/netsec/q0773"
)

nameID, err := netsec.ReadNameID(responseXML)
if err != nil {
	// 拒绝无法无歧义读取的 NameID。
}

digest, err := netsec.DigestExcludingComments(signedInfoXML, sha256.New())
```

`ReadNameID` 接受一个完整 XML 文档，并查找唯一的 `NameID` 元素。该元素必须只包含一个普通文本节点；只要出现注释、CDATA、嵌套元素、处理指令或多个文本节点，读取就会失败。首尾空白和空值也会失败。元素之外的注释不会影响读取。

`CanonicalizeExcludingComments` 按 Canonical XML 1.0 规则输出不含注释的规范形式。`DigestExcludingComments` 对该结果计算摘要，因此改变元素内部或外部的注释都不会改变摘要。摘要函数默认使用 SHA-256，也可传入 `hash.Hash`、`func() hash.Hash` 或 `crypto.Hash`。

安全边界：签名摘要忽略注释是 XML 签名的正常语义；`NameID` 读取拒绝注释是为了避免不同 XML 解析器对同一签名字节给出不同的身份值。不要把未经 `ReadNameID` 校验的值用于授权判断。
