# TCC责任进程归属与授权匹配

`netsec` 提供纯内存的 macOS 隐私授权模拟。它根据进程启动关系解析责任进程，再按
责任进程的签名标识和团队 ID 查询授权表，适合单元测试、策略回归和授权决策演练。

```go
processes := []netsec.Process{
    {PID: 100, SigningID: "com.example.photoapp", TeamID: "TEAMAAAAAA"},
    {
        PID:             200,
        ParentPID:       100,
        SigningID:       "com.example.photoapp.helper",
        TeamID:          "TEAMAAAAAA",
        AppBundleHelper: true,
    },
}
table := []netsec.Authorization{
    {Service: "kTCCServicePhotos", ClientID: "com.example.photoapp", TeamID: "TEAMAAAAAA", Allowed: true},
}

decision := netsec.Check(processes, table, 200, "kTCCServicePhotos")
if decision.Allowed {
    // 辅助程序使用应用的责任进程身份获得相册授权。
}
```

判定顺序：

1. 从请求进程沿 `ParentPID` 向上查找。`DisclaimsResponsibility` 为真的进程自行承担
   责任，不再继承祖先授权。
2. 应用包内辅助程序必须归属到其应用祖先；没有可归属应用时拒绝。
3. 请求进程未签名，或责任链上存在调试器/未签名动态库时拒绝。
4. 仅当服务、责任进程签名标识、团队 ID 三项完全一致且条目允许时放行；精确匹配的
   拒绝条目优先，默认拒绝。

未命中的授权、同标识异团队、无签名进程以及责任链被调试或注入的场景都有回归测试。
