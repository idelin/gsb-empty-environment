# Maven坐标重定向防护

攻击者在POM里声明relocation，把广泛使用的坐标重定向到自己的groupId。为Go依赖解析器实现可导入的重定向防护包：最多跟随三跳且不得成环；目标groupId与原groupId须同属调用方登记的命名空间所有者或已被显式批准；groupId转路径前拒绝空段、..和反斜杠。用跨所有者、环和路径穿越坐标测试，被拒时不得下载任何目标制品。

## 包：`example.org/netsec/q0069/relocation`

### 防护策略

- 跳数上限：每条解析链最多跟随 `DefaultMaxHops`（3）跳 relocation，超出返回 `ErrMaxHops`。
- 环检测：链中重复出现的坐标立即返回 `ErrCycle`。
- 所有者授权：目标 groupId 必须与原 groupId 同属调用方登记的命名空间所有者，或目标 groupId 已被 `Registry.Approve` 显式批准；未登记的原 groupId 一律拒绝（fail closed），跨所有者返回 `ErrCrossOwner`。
- 路径安全：`CheckGroupID` / `GroupPath` 在把 groupId 转成仓库相对路径前，拒绝空段（首尾点、连续点）、`..` 段与反斜杠。
- 零下载保证：`Guard.Download` 仅在整条链通过校验后调用下载器；任何拒绝都不会下载目标制品。

### 用法

```go
import "example.org/netsec/q0069/relocation"

registry := relocation.NewRegistry()
_ = registry.RegisterOwner("acme", "com.acme", "org.acme")
// 可选：显式批准跨所有者目标
// _ = registry.Approve("net.verified.target")

guard := &relocation.Guard{
    Registry: registry,
    Source:   pomSource,       // 实现 RelocationSource
    MaxHops:  relocation.DefaultMaxHops,
}

resolved, err := guard.Download(ctx,
    relocation.Coordinate{GroupID: "com.acme.lib", ArtifactID: "lib", Version: "1.0.0"},
    downloader,               // 实现 ArtifactDownloader
)
```

### 测试

`go test ./...` 覆盖同所有者/跨所有者/显式批准、三跳边界与超限、自环与回环、路径穿越坐标，以及所有被拒场景下下载器零调用。
