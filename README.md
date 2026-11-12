# npm标签与内容绑定

镜像服务可把dist-tag指向攻击者版本，或让tarball与元数据不符。为Go包管理代理实现可导入的npm校验包：标签解出版本后，tarball的SRI须匹配（多摘要取最强）；dist.signatures对name@version:integrity的签名经回调通过；包内name、version一致。用换标签、改tarball和错版本样例测试。

## `npmverify` 包

导入路径：`example.org/netsec/q0081/npmverify`，仅依赖 Go 标准库。

镜像（registry mirror）不可信时，代理在把包交给构建前需要把“标签 → 版本 →
tarball 字节 → 包内身份”重新绑定。`npmverify.Verify` 按顺序执行：

1. **标签解出**：`dist-tags[tag]` 必须能在 `versions` 中找到对应版本
   （`ResolveTag`，失败返回 `ErrTagNotFound`），防止标签被换到未发布版本。
2. **SRI 校验**：解析 `dist.integrity`，多摘要时按
   `sha512 > sha384 > sha256 > sha1 > md5` 取最强算法，重算 tarball 摘要并以
   常量时间比较（不匹配返回 `ErrIntegrityMismatch`）。
3. **签名校验**：对固定消息 `name@version:integrity`，逐条调用调用方提供的
   `SignatureVerifier` 回调；任一签名被可信密钥接受即通过，否则
   `ErrSignatureFailed`。默认强制要求至少一条签名（`Options.RequireSignature`
   可关闭）。
4. **包内身份一致**：从 tarball 解出 `package/package.json`（兼容根目录布局），
   其 `name`、`version` 必须与解出的元数据一致，否则 `ErrNameMismatch` /
   `ErrVersionMismatch`。

### 用法

```go
import "example.org/netsec/q0081/npmverify"

// keyring 实现 npmverify.SignatureVerifier，持有代理信任的 registry 公钥。
keyring, _ := npmverify.NewECDSAKeyring(map[string][]byte{
    "registry-2026": registryPEM,
})

result, err := npmverify.Verify(ctx, packument, "latest", tarballBytes, keyring.Verify, nil)
```

也可自行实现 `SignatureVerifier` 接入 Ed25519、HSM 或外部验签服务；包本身不做
网络请求与密钥管理。

### 测试

`go test ./...` 覆盖三类攻击样例及关键单元逻辑：

- 换标签：`TestVerifySwappedTagUntrustedSignature`（攻击者版本由未信任密钥签名）、
  `TestVerifySwappedTagMissingVersion`（标签指向不存在的版本）。
- 改 tarball：`TestVerifyTamperedTarball`（字节被改，SRI 不匹配）。
- 错版本/错名：`TestVerifyWrongVersionInTarball`、`TestVerifyWrongNameInTarball`。
- 另含多摘要取最强（`TestVerifyPicksStrongestDigest`）、SRI 解析、tarball 解析、
  ECDSA keyring 与回调的单元测试。
