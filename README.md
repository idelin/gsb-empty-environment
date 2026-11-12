# JNA库加载路径与回调生命周期

JNA 让 Java 代码加载原生库并把回调对象交给原生代码，插件若能任意指定库名或函数地址，宿主就会在攻击者选定的代码上执行；回调对象被回收后原生侧仍持有地址会形成释放后使用。为 Go 编写的插件审计网关写供 JNA 事件订阅调用的权限包：事件含库加载（库名与解析后路径）、Function.getFunction 的地址、回调注册与注销、回调触发（线程与目标回调编号）；库名只允许调用方登记的名称，规范化后路径须在登记目录内，含 ..、符号链接穿出或相对路径拒绝；函数地址须落在已加载登记库的导出符号范围内；回调在注销或回收之后被触发，报告释放后使用；原生线程未经初始化登记就调用宿主敏感方法拒绝。用路径穿越、任意地址、注销后触发、未登记线程和合规加载验证。

## 包接口

根包为 `netsec`。受信策略由宿主登记，JNA 事件视为不可信输入：

```go
gateway := netsec.NewGateway()

// 库名和目录由宿主登记。导出范围应来自宿主信任的符号表。
gateway.RegisterLibrary("plugin-native", "/opt/plugin/lib", netsec.ExportRange{
    Symbol: "plugin_entry",
    Start:  0x1000,
    End:    0x1100, // 半开区间
})
gateway.RegisterNativeThread("native-worker-1")

// JNA 订阅事件交给 Process 判定。
decision := gateway.Process(netsec.Event{
    Kind:        netsec.EventLibraryLoad,
    LibraryName: "plugin-native",
    Path:        "/opt/plugin/lib/libplugin.dylib",
})
if !decision.Allowed {
    auditLog(decision.String())
}
```

主要规则：

- `EventLibraryLoad`：库名必须精确匹配登记名；路径必须为绝对路径，原始路径任意一段含 `..` 即拒绝；词法规范化后须位于登记目录内；再经 `EvalSymlinks` 校验，防止符号链接穿出目录。相对路径、外部路径、符号链接逃逸均拒绝。
- `EventFunctionLookup`：地址必须落在**已加载**登记库的某个导出半开区间 `[Start, End)` 内；未加载库、未登记库和区间外地址均拒绝。
- `EventCallbackRegister/Unregister/Reclaim`：登记回调编号并维护生命周期。注销或回收后再次触发返回 `CodeUseAfterFree`。
- `EventCallbackTrigger`：触发线程必须已初始化登记，目标回调必须仍处于 active 状态。
- `EventNativeThreadInit` / `EventHostCall`：原生线程未初始化登记时调用宿主敏感方法返回 `CodeUnregisteredThread`。

所有拒绝都带有稳定 `Code` 和可审计的 `Reason`；`Decision.String()` 可直接写入审计日志。`RegisterLibrary`、`RegisterExport` 等策略登记接口返回 `error`，失败时的动态类型为 `*PolicyError`，可用 `CodeOf(err)` 获取稳定错误码。
