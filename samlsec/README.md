# samlsec — SAML 2.0 断言防包装 / 防注释截断验证包

`samlsec` 是只依赖 Go 标准库（`encoding/xml`、`crypto/rsa`、`crypto/ecdsa`、
`crypto/sha256`）的 SAML 2.0 Response/Assertion 安全验证组件，不联网、不
引入任何第三方 XML 签名库。

## 威胁模型与防御

| 攻击 | 防御点 |
| --- | --- |
| XXE / 十亿笑弹 / 任意 DTD | 解析前对原始字节流硬扫描，`<!DOCTYPE`、`<!ENTITY` 及其它一切 `<!` 声明直接拒绝；命名实体在无 DTD 时由解码器拒绝 |
| XSW1–XSW8（伪造未签名断言、签名移入 `Extensions`、兄弟/子节点包装、Reference 重指向） | 全文至多一个 `Assertion`、一个 `Signature`；`Reference URI` 限定裸 `#id`，被签名元素路径强制为根 `Response` 或其唯一直接子 `Assertion`，且必须等于签名承载元素 |
| `NameID` 注释 / PI 截断（`admin<!-- -->@evil.com`） | `NameID`/`Issuer`/`Audience`/`AttributeValue` 内部出现任何注释或处理指令即拒绝；文本提取只拼接文本节点，注释永不入串 |
| 重复 `ID`/`xml:id`、重复属性、命名空间标签混淆 | 解析期建立全局 ID 索引并查重，逐元素属性查重，安全关键元素强制命名空间 |
| 弱算法 / 算法替换 | 仅接受 Exc-C14N、SHA-256 摘要、RSA-PKCS1v15/PSS 与 ECDSA 的 SHA-256 签名；Transform 链必须含 enveloped + exc-c14n |
| 断言重放 | `ReplayCache` 接口 + 进程内 TTL 实现，按单次断言 `ID` 去重 |

## 验证流程

1. `Parse`：字节预扫描 → 严格 token 解析 → 自定义 DOM（保留命名空间栈、注释、PI）。
2. `verifySignature`：签名路径约束 → 算法白名单 → Reference 精确绑定 →
   Exc-C14N 重算 SHA-256 摘要 → 非对称 `SignatureValue` 验签。
3. SAML 语义：`Issuer`、`Conditions` 时间窗（含时钟偏移）、
   `AudienceRestriction`、Bearer `SubjectConfirmationData` 的
   `Recipient`/`InResponseTo`/`NotOnOrAfter`。
4. 防重放登记，再从**验签通过的同一节点子树**提取 `Issuer`/`NameID`/
   `Attributes`，封装为 `VerifiedAssertion`。

## 用法

```go
opts := samlsec.Options{
    TrustedKey:           idpPublicKey, // *rsa.PublicKey 或 *ecdsa.PublicKey
    ExpectedAudience:     "https://sp.example.com/",
    ExpectedRecipient:    "https://sp.example.com/acs",
    ExpectedInResponseTo: requestID,
    ExpectedIssuer:       "https://idp.example.com/",
    ClockSkew:            30 * time.Second,
    Replay:               samlsec.NewMemoryReplayCache(time.Hour),
}
verified, err := samlsec.Validate(rawSAMLBytes, opts)
```

错误通过 `errors.Is` 匹配哨兵（`ErrDTD`、`ErrReferenceBinding`、
`ErrDigestMismatch`、`ErrSensitiveMarkup`、`ErrReplay` 等），任何失败都
意味着该 Response 必须被拒绝。

## 测试

`go test ./...` 覆盖：合法 RSA/ECDSA 签名、XSW1–XSW8 全部变种、NameID
注释截断与敏感元素 PI、XXE/实体注入、重复 ID/属性、Exc-C14N 手算向量、
时间窗 / 受众 / Recipient / InResponseTo、防重放与算法白名单。
