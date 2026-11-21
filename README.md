# 5G 切片授权

5G核心网会话管理在切换时把旧允许切片列表整体复制给目标接入网，攻击者控制的小区可让用户会话进入未订阅切片。请实现供Go会话管理功能调用的可导入切片授权组件。订阅、注册、PDU会话和切换各自携带S-NSSAI集合，每个切片绑定SST与SD、漫游限制、DNN和QoS策略代次；切换只保留订阅与目标接入网都允许的交集，新增切片必须重新核对订阅。AMF重选、会话连续性和旧SMF上下文中的切片不得直接继承，拒绝映射到标准原因码。用订阅外切片、SD混淆、漫游限制、跨AMF重选、合法同订阅切片和部分允许场景验证不越权。

## 组件

`sliceauth` 是零第三方依赖的 Go 包（module `sliceauth`），`Authorizer` 在 `New`
后不可变，可并发调用。核心类型与入口：

- `SliceID`：归一化 S-NSSAI（SST + SD）。缺省 SD 按 TS 23.003 归一化为
  `0xFFFFFF`，只有 SST 与 SD 全等才匹配，杜绝同 SST 不同 SD 的混淆。
- `SliceSubscription`：每个切片绑定 `AllowedDNNs`、`QoSPolicyGeneration`
  与按拜访 PLMN 组织的 `RoamingRule`（含 mapped S-NSSAI 与允许/禁止位）。
- `AuthorizeRegistration`：由 requested NSSAI 重新生成 allowed NSSAI；
  入参若自带 `AllowedNSSAI`（旧 AMF 上下文继承）直接返回 Go error。
- `AuthorizePDUSession`：逐会话核对切片 ∈ 注册 allowed NSSAI、订阅、
  DNN、QoS 策略代次，SMF 上下文不作为既成授权。
- `AuthorizeHandover`：对每个 PDU 会话和新增切片依次执行
  注册 allowed NSSAI → 订阅/漫游 → DNN/QoS → 目标 RAN allowed NSSAI
  校验，只放行“订阅 ∩ 目标接入网”交集；`HandoverRequest` 刻意没有
  源 allowed NSSAI 字段，从 API 层面禁止整表复制。
- `ReselectAMF`：目标 AMF 能力只能删减已授权切片，不能注入；
  跨 AMF 切换未先重选时 `AuthorizeHandover` 返回编程错误。
- `AllowedTargetSlices`：计算可向目标节点通告的交集切片列表。

## 标准原因码映射（TS 24.501）

| 内部拒绝原因 | 5GMM（注册/移动性） | 5GSM（PDU 会话） |
| --- | --- | --- |
| 未订阅 S-NSSAI / SD 混淆 | #71 S-NSSAI not subscribed | #68 S-NSSAI not supported |
| 漫游限制 | #13 Roaming not allowed | #29 User authentication or authorization failed |
| 不在 allowed NSSAI / 继承上下文 | #71 | #68 S-NSSAI not in allowed NSSAI |
| 目标 RAN/AMF 不支持 | #71 | #26 Insufficient resources for slice+DNN |
| DNN 未订阅 | #72 | #70 Missing or unknown DNN |
| QoS 策略代次过期 | #39 Reactivation requested | #29 |
| 目标节点无任何可用切片 | #62 No network slices available | — |

策略拒绝返回 `Rejection`（含原因码与可读明细），只有畸形入参才返回 Go error。

## 验证

```sh
go test ./... -race -cover
```

表驱动测试覆盖六类不越权场景：订阅外切片（5GMM #71 / 5GSM #68）、
SD 混淆（同 SST 不同 SD 一律拒绝）、漫游限制（home-only 与显式禁止均
#13/#29）、跨 AMF 重选（只减不增、未重选禁止跨 AMF 切换）、合法同订阅
切片正常放行、部分允许（目标 RAN 交集外会话按 #26 丢弃），并额外回归
旧 SMF 上下文继承、QoS 代次过期与整表复制攻击路径。
