# netsec：按信任域路由 CI 执行器

本包为 Go 调度服务提供一个可导入的执行器路由组件，修复“作业自报特权标签即可进入生产执行器”的提权问题。

## 安全模型

执行器选择不再只看标签。一次成功路由必须同时满足：

1. **来源可信**：`Source` 来自调度控制面，不从工作流 YAML 读取；`fork` 或未知来源一律降为不可信。
2. **仓库策略可信**：仓库信任级别与受保护分支模式只来自服务端仓库注册表，作业无法声明。
3. **分支真实受保护**：`Ref` 会按 `refs/heads/*` 语法规范化；标签、PR merge ref、路径穿越、尾部斜杠、空白、大小写变体和 `branch`/`ref` 冲突都不能伪装成受保护分支。
4. **仅限本域**：作业由来源、仓库信任和分支保护共同解析出唯一信任域，只能匹配注册时声明同一域的执行器。标签和能力不能跨域借用，也不存在降级回退。
5. **特权标签/能力二次授权**：`PrivilegedLabels` 与 `PrivilegedCapabilities` 只有在“特权仓库 + 受保护分支 + 非 fork 来源”同时成立时才可请求。
6. **注册不可迁移**：执行器 ID 的域在更新、删除后重新注册时都不能改变，防止同一 ID 借壳进入其他域。

特权仓库的非受保护分支会降为 `Trusted`，因此不会落到持有生产凭据的执行器上。未知仓库、未知来源和 fork 都按 `Untrusted` 处理。

## 使用方式

```go
router, err := netsec.NewRouter(netsec.Config{
	PrivilegedLabels:       []string{"privileged", "prod-secrets"},
	PrivilegedCapabilities: []netsec.Capability{"secrets.read"},
	Repositories: []netsec.Repository{
		{ID: "acme/app", Trust: netsec.TrustPrivileged, ProtectedBranches: []string{"main", "release/*"}},
	},
})
if err != nil {
	return err
}

if err := router.RegisterExecutor(netsec.Executor{
	ID:           "prod-a",
	Domain:       netsec.DomainProduction,
	Labels:       []string{"linux", "docker", "privileged"},
	Capabilities: []netsec.Capability{"build", "secrets.read"},
}); err != nil {
	return err
}

placement, err := router.Route(netsec.Job{
	ID:                   "run-1",
	Repository:           "acme/app",
	Source:               netsec.SourceRepository, // 必须来自服务端事件元数据
	Ref:                  "refs/heads/main",
	Labels:               []string{"linux", "privileged"},
	RequiredCapabilities: []netsec.Capability{"build", "secrets.read"},
	Queue:                "build",
	CacheKey:             "deps",
	ArtifactKey:          "bundle",
})
```

`placement` 中同时返回：

- `ExecutorID`、`Domain`、`Trust` 与 `ProtectedBranch`；
- `Queue`：`queue/<domain>/<queue>`；
- `CacheNamespace`：`cache/<domain>/<repository>/<key>`；
- `ArtifactNamespace`：`artifacts/<domain>/<repository>/<key>`。

域和调用方提供的路径片段都会被转义，队列、缓存和产物不会跨域共享。路由失败时不会返回执行器；调用方应按错误拒绝作业，而不是忽略标签后继续调度：

- `ErrPrivilegedLabel` / `ErrPrivilegedCapability`：特权请求未被授权；
- `ErrInvalidRef`：引用/分支非法或互相矛盾；
- `ErrNoExecutor`：本域内没有满足不可伪造的标签和能力要求的执行器；
- `ErrDomainChange`：试图迁移执行器域。

## 信任变更

仓库信任和分支保护可在运行时更新，下一次路由立即生效：

```go
_ = router.SetRepositoryTrust("acme/app", netsec.TrustTrusted)
_ = router.SetProtectedBranches("acme/app", []string{"release/*"})
```

`Resolve` 可在不选择执行器的情况下返回审计决策（有效信任级别、域、是否受保护）。

## 验证

测试覆盖标签伪造、分支绕过、跨域借用、合法受信作业、信任下调/恢复、分支保护移除、作用域隔离以及并发路由：

```sh
go test ./...
go test -race ./...
```
