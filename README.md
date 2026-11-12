# assetlinks声明接管判定

Android App Link 靠域名下的 assetlinks.json 声明哪个应用可接管链接，攻击者发布包名相同但签名不同的应用，或在文件中混入其他关系的声明。为Go应用校验服务实现可导入的声明解析包：只认 relation 含 delegate_permission/common.handle_all_urls 且 target.namespace 为 android_app 的语句；sha256_cert_fingerprints 的十六进制按冒号分隔、大小写不敏感，须与应用签名证书之一完全相等；响应要求 https、状态 200、无重定向、Content-Type 为 application/json 且不超过 1 MiB；同一包名多条语句取并集。用错指纹、其他 relation、重定向与超限响应验证。

## 用法

```go
// 联网抓取并校验（自带 https/200/无重定向/Content-Type/1 MiB 限制）：
ok, err := netsec.VerifyURL(ctx, netsec.NewClient(),
    "https://example.com/.well-known/assetlinks.json",
    "com.example.app", appCertFingerprints)

// 或离线解析已有文档：
stmts, err := netsec.ParseStatements(doc)
ok := netsec.VerifyPackage(stmts, "com.example.app", appCertFingerprints)
```

- `assetlinks.go`：语句解析、指纹规范化（冒号分隔、大小写不敏感）、按包名取并集后与应用签名证书精确比对。
- `fetch.go`：带安全约束的抓取——仅 https、状态 200、拒绝重定向、Content-Type 须为 application/json、正文不超过 1 MiB。
