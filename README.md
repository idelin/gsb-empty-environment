# GOOSE 状态号与序号准入（gooseadmit）

`gooseadmit` 是一个可导入的 Go 包，供保护装置的二层 GOOSE 订阅回调调用，
依据 IEC 61850-8-1 的 `stNum`/`sqNum` 语义与 `timeAllowedToLive` 做准入控制，
拦截旧帧重放、伪造大 `stNum`、未登记发布者和仿真报文。

导入路径：`example.org/netsec/q0715/gooseadmit`

## 处理流程

每帧按顺序经过以下检查，任一不过即丢弃，不改动任何已保存状态：

1. **EtherType**：仅接受 `0x88B8`；自动跳过一层/两层 802.1Q（`0x8100`、`0x88A8`）标签。
2. **发布者登记**：用（源 MAC、`gocbRef`、`datSet`、`goID`）四元组定位发布者，
   未登记的组合一律 `DropUnregistered`，因此伪造身份或大 `stNum` 的帧在状态机之前就被丢弃。
3. **simulation 位**：simulation=1 的帧只有在订阅端 `TestMode` 打开时才接受，
   生产模式返回 `DropSimulation`；测试模式可在运行时通过 `SetTestMode` 切换。
4. **序号状态机**（见下表）。
5. **存活判定**：用帧内 `timeAllowedToLive`（毫秒）维护存活状态，
   超时未收到被接受的新帧则数据失效。

## stNum / sqNum 状态机

| 情况 | 判定 | 结果 |
| --- | --- | --- |
| 登记后的首帧 | 建立基线 | `VerdictNewEvent` |
| `stNum` 变大且 `sqNum == 0` | 新事件 | `VerdictNewEvent` |
| `stNum` 变大但 `sqNum != 0` | 伪造/畸形 | `DropStNumWithoutSqZero` |
| `stNum` 相等、`sqNum` 变大 | 正常重发 | `VerdictRetransmission` |
| `stNum` 相等、`sqNum` 相等 | 重复帧 | `VerdictDuplicate`（忽略，不刷新存活时间） |
| `stNum` 相等、`sqNum` 变小 | 重放 | `DropSqReplay` |
| 上一 `sqNum == 2^32-1` 且本帧为 0 | 32 位回绕 | `VerdictRetransmission` |
| 其他位置的 `sqNum` 回 0 | 伪装回绕的重放 | `DropSqReplay` |
| `stNum` 变小 | 旧帧重放 | `DropStNumDecreased` |
| 已调用 `NotifyRestart` 后的下一帧 | 发布者重启 | 重建基线，`VerdictNewEvent`，通告一次性失效 |

重启通告是显式、一次性的“带外”动作，用于发布 IED 重启后 `stNum` 从小值重新开始的
合法场景；没有通告时 `stNum` 变小永远丢弃，且序号基线在丢弃/失效期间保留，
重放帧无法借此复活数据。

## timeAllowedToLive 失效

- 每帧携带的 TTL 以毫秒解释，`IsValid` 按需判定，`Sweep` 可由定时器周期调用并返回刚失效的发布者。
- 只有被**接受**的新事件/重发会刷新 `lastSeen`；重复帧不刷新，防止循环重放续命。
- 失效只置标志位，不清空 `stNum`/`sqNum` 基线；之后合法的更新帧会自动恢复有效。

## 集成方式

```go
import "example.org/netsec/q0715/gooseadmit"

ctrl := gooseadmit.New(&gooseadmit.Config{TestMode: false}) // 生产订阅端
ctrl.Register(gooseadmit.NewPublisherKey(mac, gocbRef, datSet, goID))

// 周期检查 timeAllowedToLive 失效
go func() {
    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()
    for range ticker.C {
        for range ctrl.Sweep() { // 处理刚失效的发布者，例如置安全态
        }
    }
}()

// 二层抓包回调：raw 为完整以太网帧
func onFrame(raw []byte) {
    switch res := ctrl.HandleFrame(raw); res.Verdict {
    case gooseadmit.VerdictNewEvent:
        // 应用新状态
    case gooseadmit.VerdictRetransmission:
        // 刷新当前数据，无状态变化
    case gooseadmit.VerdictDuplicate:
        // 忽略
    default:
        // res.Reason 记录丢弃原因，可上报告警
    }
}
```

发布者重启时（例如收到运维信号）调用 `ctrl.NotifyRestart(key)`，下一个该发布者的帧
即允许较小的 `stNum` 并重建基线。

## 测试

`go test ./gooseadmit -race -count=1` 覆盖：

- `stNum` 跳变但 `sqNum` 不置零的丢弃，以及基线不被污染；
- `sqNum` 相等重复、变小重放、`2^32-1 → 0` 合法回绕与伪装回绕；
- `stNum` 变小丢弃、`NotifyRestart` 一次性放行；
- TTL 过期失效、重复帧不能续命、合法新帧恢复；
- simulation 位在生产/测试模式及运行时切换下的行为；
- EtherType、VLAN 标签、截断帧、未登记四元组与伪造 MAC/gocbRef。

## 边界说明

该包提供的是序号与身份层面的准入，不替代密码学认证。若需要对抗具备报文伪造能力的
攻击者，应同时启用 IEC 61850 GOOSE 签名等机制；`Config.Now` 可注入时钟以便测试与
确定性仿真。
