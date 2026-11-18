# JPMS反射开放判定

Java 模块强封装下，反射访问非公开成员要看包是否对调用者模块 opens，攻击者借自动模块、限定 opens 或残留的 add-opens 把内部包暴露给插件。为Go宿主实现供插件加载器调用的模块开放判定包：输入模块描述（requires 含 transitive、exports 与 opens 含 to 限定列表、open module、automatic module）及启动时的 add-opens 补丁，判定模块 A 对 B 的包 p 能否 setAccessible；A 须可读 B（含 requires transitive 传递），opens 须无限定或点名 A；未命名模块与自动模块的包全部开放。用限定 opens 点名他人、transitive 链、open module 与补丁覆盖验证。

## 判定模型

`Layer.CanSetAccessible(caller, target, pkg)` 按 JPMS 规则分两阶段判定，模块名传空串表示未命名模块：

1. 可读性（`CanRead`）：自身恒可读；未命名模块读所有模块；自动模块读所有模块；自身的 `requires`（含 static）建立直接可读，后续层级只沿 `requires transitive` 边传播（普通 requires 不会被再导出）。命名模块默认不读未命名模块。
2. 开放度（`IsOpen`）：未命名模块、自动模块、open module 的全部包开放；普通模块看 `opens` 指令——无限定则对所有模块开放，有限定则只对 `to` 列表中的模块开放；`exports` 不授予深度反射权限。
3. `--add-opens` 补丁按包强制开放：点名调用者或 `ALL-UNNAMED`（仅匹配未命名调用者）。点名命名模块时同时为其补一条 read 边，与 JVM 行为一致。

## 用法

```go
patches, err := netsec.ParseAddOpensAll("--add-opens java.base/java.lang=ALL-UNNAMED")
if err != nil {
    log.Fatal(err)
}

layer := netsec.NewLayer([]netsec.ModuleDescriptor{
    {
        Name:     "plugin",
        Requires: []netsec.RequireDirective{{Module: "core"}},
    },
    {
        Name: "core",
        Opens: []netsec.PackageDirective{
            {Package: "core.internal", Targets: []string{"trusted"}},
        },
    },
}, patches)

// false：core.internal 只对 trusted 开放，plugin 未被点名
layer.CanSetAccessible("plugin", "core", "core.internal")
```

## 验证

`go test ./...` 覆盖限定 opens 点名他人、transitive 传递链、open/automatic/未命名模块、`ALL-UNNAMED` 与命名目标补丁覆盖（含补丁同时补齐可读性）等场景。
