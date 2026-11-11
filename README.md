# depseal

构建服务安装依赖时发现锁文件只固定了版本，私有源故障后会从公网取同名包。用Go实现供安装器调用的锁文件解析和获取包，绑定源身份、内容摘要与父依赖；并行请求可共享已校验内容，无权访问的租户不可借缓存绕过校验。用本地测试源复现跨源重定向与错误摘要重试。

## 设计

`depseal` 是一个只依赖 Go 标准库的包，安装器通过 `ParseLockfile` 解析锁文件，
通过 `Fetcher` 获取并校验包。

锁文件中的每个包都绑定三项安全信息：

- **源身份（source）**：包只能从锁文件声明的源请求；源定义了 `base_url` 和
  `allowed_hosts`。HTTP 重定向的每一跳都经过 `CheckRedirect` 校验，目标主机
  不在允许集合内时直接返回 `CrossSourceError`，私有源因此无法把同名包请求
  静默重定向到公网注册表。
- **内容摘要（digest）**：仅支持 `sha256:<hex>`。下载内容必须先通过摘要校验，
  校验通过的 blob 才会写入缓存；摘要不匹配（`DigestMismatchError`）、5xx、
  408、429 等临时故障按指数退避重试，403/404 等 4xx 与跨源错误立即失败。
- **父依赖（parent）**：以 `name@version` 指向引入该包的直接父依赖，根依赖留空。
  解析时校验父链完整且无环，`Lockfile.DependencyPath` 可还原完整来源链，
  获取失败时 `FetchError` 会带上父依赖身份。

并行与租户隔离：

- 缓存键为 `(tenant, sourceID, digest)`，且只在摘要校验通过后写入；同一租户对
  同一 blob 的并发请求通过 singleflight 合并为一次下载，等待方共享已校验内容，
  返回给调用方的字节切片都会复制，调用方无法污染共享缓存。
- `Authorizer` 在缓存查询之前执行。未授权租户对某个源的请求直接得到
  `TenantDeniedError`，缓存中也不存在跨租户的键，无法借其他租户的缓存绕过
  源授权与摘要校验。

## 锁文件示例

```json
{
  "version": 1,
  "sources": {
    "private": {
      "id": "private",
      "base_url": "https://registry.internal.example",
      "allowed_hosts": ["registry.internal.example", "mirror.internal.example"]
    }
  },
  "packages": [
    {"name": "app/root", "version": "3.0.0", "source": "private",
     "digest": "sha256:bcafefe3..."},
    {"name": "lib/internal", "version": "1.2.0", "source": "private",
     "digest": "sha256:...", "parent": "app/root@3.0.0"}
  ]
}
```

## 安装器调用方式

```go
lock, err := depseal.ParseLockfile(reader)
authz := depseal.NewStaticAuthorizer(map[string][]string{
    "tenant-a": {"private"},
})
fetcher := depseal.NewFetcher(lock, authz)

// 单个包
res, err := fetcher.Fetch(ctx, "tenant-a", "app/root", "3.0.0")

// 并行批量获取所有锁定包，自动共享已校验内容
results, err := fetcher.FetchAll(ctx, "tenant-a", 8)
```

## 测试

测试使用进程内 `http.Handler` 与自定义 `RoundTripper` 模拟本地测试源，
不监听任何 TCP 端口，重定向仍由标准 `http.Client` 与 `CheckRedirect`
真实执行：

- `TestCrossSourceRedirectBlocked`：私有源 302 到公网同名源，重定向被拦截，
  公网源零请求；`TestAllowedMirrorRedirect` 验证白名单镜像可正常跳转。
- `TestDigestMismatchRetriesThenSucceeds` / `TestDigestMismatchExhaustsRetries`：
  错误摘要内容被拒绝并重试，成功后才入缓存，持续错误绝不返回或缓存。
- `TestTenantCannotBorrowCache`：未授权租户即使在另一租户已缓存 blob 后，
  仍被拒绝且无跨租户缓存键。
- `TestParallelFetchSharesDownload`：8 个并发请求只触发一次下载并各自获得
  独立的内容副本。

```sh
go test -race -count=1 ./...
```
