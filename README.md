# CocoaPods CDN规范校验

Pod 客户端经 CDN 取 podspec，攻击者替换 CDN 上的 podspec 把 source 指向恶意地址，或在私有源与公共源里发同名 Pod。为Go镜像服务实现可导入的 podspec 校验包：Specs 路径按 MD5(名称) 的前三个十六进制字符各一层目录生成，如 Specs/a/b/c/Name/1.0/Name.podspec.json；podspec.json 的 SHA1 须与 Podfile.lock 的 SPEC CHECKSUMS 一致；Pod 必须来自 lock 中 SPEC REPOS 记录的那个源，同名出现在多个源时以 lock 记录为准而不是 Podfile 的源顺序；source 的 http 地址拒绝，git 源须固定到 40 位提交。用改 source、同名跨源、路径分片和 http 源验证。

## 校验顺序

`Verifier.Verify` / `Verifier.VerifyFS` 对每个 podspec 依次执行：

1. **路径分片**：由 `MD5(name)` 前三个十六进制字符生成
   `Specs/<c0>/<c1>/<c2>/<Name>/<Version>/<Name>.podspec.json`
   （见 `SpecPath`），路径不符直接拒绝，同时拦截含 `..`/路径分隔符的名称。
2. **SHA1 校验**：对 podspec 原始字节算 SHA1，与 `SPEC CHECKSUMS` 比较（大小写不敏感）。
   篡改 source 会改变字节，在此步失败，返回 `ErrChecksumMismatch`。
3. **源归属校验**：取 pod 实际所在 repo key，与 `SPEC REPOS` 中该 pod 记录的源比较。
   同名 Pod 出现在多个源时以 lock 记录为准，与 Podfile 源顺序无关，不符返回
   `ErrSpecRepoMismatch`。
4. **source 策略**：`:http`（含 https 归档地址）一律拒绝（`ErrHTTPSource`）；
   `:git` 必须带 40 位十六进制 `:commit`，tag/branch/短 SHA 均拒绝
   （`ErrGitCommitNotPinned`），git 的明文 http URL 与其他 source 类型返回
   `ErrUnsupportedSource`。

## 导入与使用

```go
import "example.org/netsec/q0433"

lock, err := netsec.ParseLockfile(lockReader)   // 解析 Podfile.lock
if err != nil { /* ... */ }

verifier := netsec.NewVerifier(lock)

// 直接对镜像读取到的字节与请求路径校验
spec, err := verifier.Verify(repoKey, relPath, podspecBytes)

// 或通过 fs.FS（如 os.DirFS(specsRoot)）按路径读取后校验
spec, err = verifier.VerifyFS(fsys, repoKey, relPath)

switch {
case errors.Is(err, netsec.ErrChecksumMismatch):   // 内容被篡改
case errors.Is(err, netsec.ErrSpecRepoMismatch):   // 同名跨源
case errors.Is(err, netsec.ErrSpecPathMismatch):   // 分片路径不符
case errors.Is(err, netsec.ErrHTTPSource):         // http 源
case errors.Is(err, netsec.ErrGitCommitNotPinned): // git 未固定提交
}
```

## 测试

`go test ./...` 覆盖改 source（checksum 失败）、同名跨源（repo 失败）、
路径分片（含错误 shard/版本/文件名）、http 源与 git 提交固定等场景。
