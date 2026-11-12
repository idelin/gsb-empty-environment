# dsc明文签名块与哈希链

源码包的信任链是 InRelease 到 Sources 索引再到 .dsc 再到各 tar 包，攻击者在 PGP 明文签名块之外追加 Files 字段，或让 Files 与 Checksums-Sha256 记录不同的哈希。为Go镜像组件实现可导入的 .dsc 解析与链校验包：只有明文签名块内的字段才算，块前后多余文本一律拒绝，dash 转义要还原；Files 与 Checksums-* 中同名文件的大小须一致且所有哈希都匹配实际文件；文件名不得含 / 或以 . 开头；.dsc 自身的 sha256 须出现在 Sources 索引里。签名校验通过调用方注入的接口完成。用块外字段、哈希不一致和重复文件名验证。

## 实现

可导入包位于 `dsc/`（导入路径 `example.org/netsec/q0400/dsc`），仅依赖标准库。

- `dsc.Parse(ctx, data, verifier)`：校验 clearsign 装甲结构（块外任何字节都返回 `ErrOutsideBlock`，仅允许文件末尾一个换行），通过调用方实现的 `dsc.Verifier` 接口完成签名验证，再解析签名块内的控制字段并合并 `Files` / `Checksums-Sha256` / `Checksums-Sha1`。
- dash 转义（RFC 4880 第 7.1 节的 `- ` 前缀）在字段解析前还原；裸 `-` 开头的行按畸形处理。
- 同名文件在任一字段中重复、文件名含 `/` 或以 `.` 开头、跨字段大小不一致、`Checksums-*` 覆盖不全或引用 `Files` 之外的文件，均在解析期拒绝。
- `(*DSC).VerifyFiles(dir)`：对实际文件做 `Lstat`（拒绝符号链接/非普通文件），单次读取同时计算 md5/sha1/sha256 并与记录的大小和摘要逐一比对。
- `(*DSC).VerifyInSources(sources, dscName)`：计算原始 .dsc 字节的 sha256，要求 Sources 索引某段落以同名发布相同摘要；`VerifyChain` 串联两步。

签名验证接口由调用方注入，例如：

```go
v := dsc.VerifierFunc(func(ctx context.Context, message []byte) error {
    // message 是完整的 clearsign 装甲块，按本仓库的密钥策略验证
    return openpgpVerifier.Verify(message)
})
d, err := dsc.Parse(ctx, dscBytes, v)
if err != nil { /* errors.Is(err, dsc.ErrOutsideBlock / ErrSignature / ...) */ }
if err := d.VerifyChain(dir, sourcesIndex, "pkg_1.0-1.dsc"); err != nil {
    return err
}
```

测试覆盖块外/装甲内字段注入、哈希与大小不一致、`Files` 与 `Checksums-Sha256` 重复文件名、非法文件名、符号链接引用、Sources 锚定失败等场景：`go test ./...`。
