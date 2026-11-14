# 切换时把旧允许切片原样复制过去

5G核心网会话管理在切换时把旧允许切片列表整体复制给目标接入网，攻击者控制的小区可让用户会话进入未订阅切片。请实现供Go会话管理功能调用的可导入切片授权组件。订阅、注册、PDU会话和切换各自携带S-NSSAI集合，每个切片绑定SST与SD、漫游限制、DNN和QoS策略代次；切换只保留订阅与目标接入网都允许的交集，新增切片必须重新核对订阅。AMF重选、会话连续性和旧SMF上下文中的切片不得直接继承，拒绝映射到标准原因码。用订阅外切片、SD混淆、漫游限制、跨AMF重选、合法同订阅切片和部分允许场景验证不越权。

## 实现

包 `example.org/netsec/q0237` 提供无外部依赖的切片授权组件。授权以订阅策略为唯一事实来源：

- S-NSSAI 由 `SST` 和 3 字节 `SD` 共同标识；SD 必须为 6 位十六进制，`"000001"`、`"000002"`、`"1"` 和空 SD 互不等价。
- 切换候选来自当前 PDU 会话和显式请求；结果再与订阅、注册允许集以及目标接入网允许集求交。
- 旧允许列表和旧 SMF 上下文只可能作为候选，绝不会直接写入结果；`AMFReselected`、`SessionContinuity` 会强制重新授权。
- 订阅策略可绑定漫游许可/PLMN allow-list、DNN 和 QoS 策略代次。
- 拒绝结果携带 5GSM/5GMM 标准原因码和稳定的 `RejectionReason`。

## 使用

```go
sub := netsec.Subscription{Slices: []netsec.SlicePolicy{{
    SNSSAI:         netsec.SNSSAI{SST: 1, SD: "000001"},
    RoamingAllowed: false,
    DNN:            "internet",
    QoSGeneration:  7,
}}}

decision := netsec.AuthorizeHandover(netsec.HandoverRequest{
    Subscription:   sub,
    Registration:   netsec.Registration{AllowedSlices: []netsec.SNSSAI{{SST: 1, SD: "000001"}}},
    PDUSession:     netsec.PDUSession{Slices: []netsec.SNSSAI{{SST: 1, SD: "000001"}}, DNN: "internet", QoSGeneration: 7},
    TargetAllowedSlices: []netsec.SNSSAI{{SST: 1, SD: "000001"}, {SST: 2, SD: "000002"}},
    ServingNetwork: netsec.ServingNetwork{HomePLMN: "310410", ServingPLMN: "310410"},
})

// decision.Allowed 仅包含订阅、注册和目标接入网都允许的切片。
// decision.Rejected 中每项都带 Cause 和 Reason。
```

`Authorizer`/`NewAuthorizer` 可保存默认订阅；请求自身携带订阅时优先使用请求值。

## 原因码

| 场景 | 原因码 |
| --- | --- |
| 切片或 SST/SD 对不在订阅中 | 5GSM #33 `CauseRequestedServiceOptionNotSubscribed` |
| 切片不在注册或目标接入网允许集 | 5GSM #32 `CauseServiceOptionNotSupported` |
| 漫游不允许 | 5GMM #13 `CauseRoamingNotAllowedInTrackingArea` |
| DNN 绑定不匹配 | 5GSM #27 `CauseMissingOrUnknownDNN` |
| QoS 策略代次不匹配、配置冲突 | 5GSM #31 `CauseRequestRejectedUnspecified` |
| 注册没有任何可用切片 | 5GMM #62 `CauseNoNetworkSlicesAvailable` |

## 验证

`go test ./...` 覆盖订阅外切片、SD 混淆、漫游限制、跨 AMF 重选/旧 SMF 上下文、合法同订阅切片、部分允许、新增切片复核以及 DNN/QoS 绑定。
