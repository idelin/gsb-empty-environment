# JPMS反射开放判定

Java 模块强封装下，反射访问非公开成员要看包是否对调用者模块 opens，攻击者借自动模块、限定 opens 或残留的 add-opens 把内部包暴露给插件。为Go宿主实现供插件加载器调用的模块开放判定包：输入模块描述（requires 含 transitive、exports 与 opens 含 to 限定列表、open module、automatic module）及启动时的 add-opens 补丁，判定模块 A 对 B 的包 p 能否 setAccessible；A 须可读 B（含 requires transitive 传递），opens 须无限定或点名 A；未命名模块与自动模块的包全部开放。用限定 opens 点名他人、transitive 链、open module 与补丁覆盖验证。

## 用法

```go
u := netsec.NewUniverse()
_ = u.Add(netsec.Descriptor{
    Name:     "java.base",
    Packages: []string{"java.lang", "jdk.internal.misc"},
    Opens:    []netsec.PackageDirective{{Package: "java.lang", To: []string{"plugin"}}},
})
_ = u.Add(netsec.Descriptor{
    Name:     "plugin",
    Requires: []netsec.Require{{Module: "java.base"}},
})
_ = u.AddPatch(netsec.AddOpensPatch{
    Source: "java.base", Package: "jdk.internal.misc",
    Targets: []string{netsec.AllUnnamed},
})

// 限定 opens 点名 plugin，且 plugin 可读 java.base → true
u.CanSetAccessible("plugin", "java.base", "java.lang")
// 未命名模块读取所有模块，ALL-UNNAMED 补丁命中 → true
u.CanSetAccessible(netsec.UnnamedModule, "java.base", "jdk.internal.misc")
```

判定对未知模块/包采取失败关闭（返回 false）；`Validate` 可在启动期
显式发现描述符中无法解析的 requires、限定目标或包归属错误。
