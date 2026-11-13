# PTP主时钟选举夺取防护

时间同步网络里，攻击者发出 priority1 为 0 的 Announce 报文，就能在最佳主时钟选举中胜出，把整个网络的时钟拨走。为Go边界时钟写可导入的主时钟选举与准入包，输入 Announce 报文字段（grandmasterPriority1、grandmasterClockClass、grandmasterClockAccuracy、offsetScaledLogVariance、grandmasterPriority2、grandmasterIdentity、stepsRemoved、源端口标识）、调用方的授权主时钟标识集合和认证回调：先过准入，源端口认证失败、grandmasterIdentity 不在授权集合内、stepsRemoved 大于等于 255 的报文丢弃，不参与选举；其余报文依次按 priority1、clockClass、clockAccuracy、offsetScaledLogVariance、priority2 数值小者胜，最后以 grandmasterIdentity 的字节序小者决胜；同一主时钟经不同路径到达时，stepsRemoved 小者优先，再比较源端口标识；4 秒内未收到 Announce 的主时钟移出候选。用未授权的 priority1=0、认证失败、stepsRemoved 越界、并列决胜和超时淘汰验证。

## 包：`bmc`

可导入路径为 `example.org/netsec/q0759/bmc`，实现带准入控制的最佳主时钟选举（BMCA），核心类型与方法见 `bmc/bmc.go`。

### 准入（按顺序，任一失败即丢弃且不改变候选集）

1. 源端口认证回调 `Authenticator` 返回 false（回调为 nil 时失败闭合，拒绝一切报文）；
2. `GrandmasterIdentity` 不在构造时传入的授权标识集合内；
3. `StepsRemoved >= 255`（常量 `MaxStepsRemoved`）。

### 选举排序（总顺序，`compareCandidate`）

依次比较以下字段，小者胜：

1. `GrandmasterPriority1`
2. `GrandmasterClockClass`
3. `GrandmasterClockAccuracy`
4. `GrandmasterOffsetScaledLogVariance`
5. `GrandmasterPriority2`
6. `GrandmasterIdentity` 字节序
7. 同一主时钟的不同路径：`StepsRemoved`，再比源端口标识（时钟标识字节序，再比端口号）

### 超时淘汰

候选按源端口标识保存，记录最后收到时间。超过 `AnnounceTimeout`（4 秒）未刷新的条目在 `Elect`、`Candidates`、`Sweep` 时淘汰；也可用 `StartSweeper` 启动后台周期清理。

### 用法

```go
elector := bmc.NewElector(
    []bmc.ClockIdentity{authorizedGM},
    func(source bmc.PortIdentity) bool { return verifyPort(source) },
)

if admitted, reason := elector.HandleAnnounce(msg); !admitted {
    log.Printf("announce discarded: %s", reason)
    return
}
best := elector.Elect() // 4 秒无 Announce 的主时钟已被移出，无候选时返回 nil
```

### 验证

`go test -race ./bmc/` 覆盖：未授权且 `priority1=0` 的攻击报文、认证失败、`stepsRemoved` 255/60000 越界与 254 边界、五个向量字段与标识字节序的并列决胜、同主时钟路径（stepsRemoved/源端口）决胜、4 秒超时淘汰，以及拒绝报文不刷新陈旧条目。
