# 镜像层与证明主体匹配

`netsec` 是可直接导入 Go 准入控制器的 OCI 镜像核对包。它不信任镜像标签，
只核对部署时拿到的原始 manifest 字节、config、压缩层和 in-toto 证明。

## 核对内容

- in-toto statement 的 `subject` 中必须有 `sha256` 等于所部署 manifest
  原始字节的摘要。把 subject 换成 layer、config 或其他 manifest 均会失败。
- manifest 的 config 描述符必须匹配 config blob 的 `sha256` 和 `size`。
- 每个 manifest layer 描述符必须匹配对应压缩 blob 的 `sha256` 和 `size`。
- gzip 层必须能完整解压到流结尾，CRC/长度校验失败即拒绝。
- 解压后层摘要必须与 `config.rootfs.diff_ids[i]` 按相同顺序一一对应；
  diff ID 数量、层数量或顺序不一致均会失败。

## 用法

```go
result, err := netsec.VerifyWithOptions(netsec.Image{
	Manifest: manifestBytes, // registry 中的原始 manifest 字节
	Config:   configBytes,
	Layers:   compressedLayers, // 与 manifest.layers 同序
}, statementOrDSSE, netsec.Options{
	ExpectedManifestDigest: deployedDigest, // 可选，准入请求中观察到的摘要
})
if err != nil {
	// 可用 errors.Is(err, netsec.ErrSubjectMissing) 等哨兵错误做策略映射。
}
_ = result
```

`attestation` 可以是原始 in-toto statement，也可以是 payload type 为
`application/vnd.in-toto+json` 的 DSSE envelope。**本包不验证 DSSE 签名**；
调用方必须先用可信公钥完成签名验证，再把已认证的 statement 交给本包。
本包负责把已认证的 statement 绑定到实际部署的 manifest。

目前支持 gzip 与未压缩 tar 层；zstd 层会以
`ErrUnsupportedMediaType` 拒绝，避免把压缩摘要误当成 diff ID。

## 测试

```sh
go test ./...
```

测试覆盖正常镜像，以及换 subject、少层、层序互换、伪造/截断 gzip、
config blob 摘要不匹配和预期 manifest 摘要不匹配等样例。

攻击者复用其他镜像的attestation，或把subject写成层或配置摘要。为Go准入控制器实现可导入的OCI镜像核对包：in-toto subject须含所部署manifest的sha256；config.rootfs.diff_ids须与逐层解压后摘要一一对应，layers压缩摘要同样核对。用换subject、少层、层序互换和伪造gzip样例测试。
