# 签名属性必需集校验

SignedData里，攻击者可追加未签名内容，或让缺少message-digest的签名属性被接受。为Go文件验签包实现RFC 5652签名者校验，供归档导入调用：signedAttrs存在时须含content-type与message-digest各一次，验签输入把隐式标签A0换成SET标签31后重新DER编码，content-type须与封装内容类型一致。用缺属性、重复属性和改摘要验证。

## 实现

`netsec` 包对 DER 编码的 RFC 5652 CMS SignedData 执行签名者校验：

- `VerifySignedData(data, detachedContent)`：校验全部 SignerInfo，返回验签通过的签名证书；detached 签名通过第二个参数传入内容，attached 签名传 `nil`。
- `VerifySignedDataWithRoots`：在上述基础上追加证书链校验。

### 强制的 signedAttrs 规则（RFC 5652 §5.3–5.5）

- signedAttrs 存在时，必须包含且仅包含一次 `content-type`（OID 1.2.840.113549.1.9.3）与 `message-digest`（OID 1.2.840.113549.1.9.4）；任何属性 OID 重复都返回 `ErrDuplicateSignedAttribute`。
- 验签输入取 signedAttrs 的原始 DER，仅把首字节隐式标签 `0xA0` 换成通用 SET 标签 `0x31`，长度与内容字节保持不变（`canonicalSignedAttrs`）。
- `content-type` 属性值必须与 `EncapsulatedContentInfo.eContentType` 完全一致，否则返回 `ErrContentTypeMismatch`。
- `message-digest` 属性值必须等于 eContent 原始字节（OCTET STRING 内容）的摘要，否则返回 `ErrDigestMismatch`；追加/替换未签名内容因此无法通过校验。
- 无 signedAttrs 时按 §5.5 直接对封装内容做验签。

支持的算法：摘要仅接受 SHA-224/256/384/512（拒绝 MD5/SHA-1）；签名支持 RSA PKCS#1 v1.5、RSA-PSS（参数必须显式给出）、ECDSA 与 Ed25519。

### 测试

`go test ./...` 覆盖：正常用例（RSA/ECDSA/Ed25519）、缺 content-type、缺 message-digest、重复属性、篡改摘要、篡改/追加封装内容、content-type 不一致、A0/31 标签误用、篡改签名、detached 内容、无 signedAttrs、RSA-PSS、SKI 形式 SID 及非法 DER。
