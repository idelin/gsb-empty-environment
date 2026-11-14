# replace指令让模块校验和失效

构建服务从代理拉取模块时，`go.mod`里的replace把依赖指向另一个源，而go.sum只校验原模块，攻击者可注入未校验代码。请实现供Go构建服务调用的可导入依赖校验组件。解析go.mod的require、replace与exclude，每个生效模块都绑定模块路径、版本、实际内容摘要与来源；replace不得绕过校验和，局部替换按路径与版本显式登记，通配替换默认拒绝。校验和缺失、来源协议不符和多个replace竞争同一模块按失败处理。用replace换源、版本伪装、代理重定向、缺失go.sum、合法本地替换和正常require验证构建只使用已校验内容。

## depverify

可导入组件位于 `depverify` 包，仅依赖 Go 标准库，离线可测。

### 能力

- 解析 `go.mod` 的 `require` / `replace` / `exclude`（含块语法、注释、字符串与 `// indirect`），以及 `go.sum` 的 `h1:` 哈希。
- 每个生效模块绑定模块路径、版本、实际内容摘要（与 `golang.org/x/mod/sumdb/dirhash` 兼容的 `h1:` 值）和来源（含完整重定向链）。
- `replace` 换源后按“生效 path@version”重新查 `go.sum`，源切换无法借原始模块的哈希蒙混过关；哈希缺失/不一致即失败，且在任何网络抓取之前拦截。
- 本地目录替换必须在 `Options.LocalReplacements` 中按模块路径 + 精确版本显式登记，并拒绝指向登记目录之外的符号链接。
- 通配（path-only）`replace` 默认拒绝；同一模块被多个 `replace` 竞争、被 `exclude` 的版本、重复 `require` 均按失败处理。
- 来源协议默认仅允许 `https`，初始 URL 与每次重定向跳变都校验，`https -> http` 降级直接失败。
- `BuildGate` 在编译前确认构建实际取用的每个模块都对应已验证摘要，未验证或被篡改的内容无法进入构建。

### 集成入口

```go
v := depverify.New(depverify.Options{
    AllowedProtocols:  []string{"https"},
    LocalReplacements: []depverify.LocalReplacement{
        {Path: "example.com/lib", Version: "v1.0.0", Dir: "/abs/local/lib"},
    },
})

// fetcher 实现 depverify.Fetcher，返回模块 zip、go.mod 与来源/重定向链。
report, err := v.Verify(ctx, goModText, goSumText, fetcher)
if err != nil {
    return err // *depverify.MultiError，聚合全部失败
}

gate := depverify.NewBuildGate(report)
for _, m := range checkout { // 构建实际取出的模块及其当前摘要
    if err := gate.Use(m.Path, m.EffectiveVersion, m.Digest); err != nil {
        return err
    }
}
return gate.Complete()
```

### 验证场景

`depverify/*_test.go` 覆盖：replace 换源绕过、版本/内容伪装、代理重定向降级、缺失 `go.sum`、合法本地替换（含篡改与符号链接逃逸）、通配与竞争 replace、exclude、重复 require 以及正常 require 的端到端门禁。运行：

```sh
go test ./...
```
