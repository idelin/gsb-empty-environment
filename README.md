# NuGet锁文件contentHash核对

`packages.lock.json` 记录每个包的 resolved 版本与 contentHash。攻击者可能改动已发布版本的内容，或在依赖图里塞入无人引用的包。本仓库是一个可导入的 Go 核对包（`example.org/netsec/q0511`，包名 `netsec`），供 Go 构建网关在还原/构建前强制校验锁文件。

## 校验规则

按目标框架逐项检查，所有问题一次性返回（`VerifyErrors`，每条带稳定错误码）：

- 条目类型必须是 `Direct`、`Transitive` 或 `CentralTransitive`，且必须包含 `contentHash`（缺失即 `E_HASH_MISSING`）。
- `Direct` 条目必须出现在调用方声明的根框架依赖里（`E_DIRECT_NOT_IN_ROOT`）。
- `resolved` 必须满足 `requested` 的 NuGet 版本区间（含 `1.2.*` 这类 floating 通配）；越界报 `E_VERSION_OUT_OF_RANGE`，低于下限报 `E_VERSION_DOWNGRADE`。
- `contentHash` 必须是标准 base64（RFC 4648、带填充、可往返规范化）；URL-safe 字母表（`-`/`_`）、缺填充等一律 `E_HASH_ENCODING`，先于哈希比对拒绝。
- `contentHash` 必须与调用方给出的实际 `.nupkg` 的 SHA-512 标准 base64 完全一致，否则 `E_HASH_MISMATCH`；调用方拿不到实际包时报 `E_HASH_UNAVAILABLE`。
- 同一包 ID（不分大小写）在同一框架下出现两条 resolved 不同的条目报 `E_DUPLICATE_RESOLVED`；跨框架允许不同。
- 非 Direct 条目必须被同框架其他条目的 `dependencies` 引用，孤立包报 `E_ORPHAN_ENTRY`；依赖目标不存在报 `E_DEPENDENCY_MISSING`。
- 依赖图无环（`E_DEPENDENCY_CYCLE`），且所有条目从 Direct 根可达，不可达报 `E_CLOSURE_UNREACHABLE`。

## 用法

```go
import netsec "example.org/netsec/q0511"

lf, err := netsec.ParseLockFile(f) // f 为 packages.lock.json

// 方式一：网关自行提供每个 .nupkg 的 SHA-512 标准 base64
err = netsec.Verify(lf, netsec.Options{
    ActualHash: func(packageID, resolvedVersion string) (string, bool) {
        return hashFromPackageCache(packageID, resolvedVersion), true
    },
    RootDependencies: map[string][]string{"net8.0": {"App.Core"}},
})

// 方式二：把“包 → 本地 .nupkg 路径”的解析器适配成 HashFunc
hashFunc := netsec.NupkgHashFunc(func(id, version string) (string, bool) {
    return pathInLocalCache(id, version), true
})
err = netsec.Verify(lf, netsec.Options{ActualHash: hashFunc, RootDependencies: roots})

if verrs, ok := err.(netsec.VerifyErrors); ok {
    if verrs.HasCode(netsec.ErrHashMismatch) { // 按错误码分支处理
    }
}
```

辅助 API：`SHA512Base64(io.Reader)` 计算与锁文件同格式的哈希；`VerifyNupkgHash(reader, expected)` 做单包快速核对。

## 测试

```sh
go test ./...        # 含越界/降级/哈希不符/URL-safe/跨框架/孤立/不可达/环等场景
go test -race ./...
```
