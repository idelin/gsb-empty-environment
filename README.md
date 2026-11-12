# 安全标记降级可达性

主机策略允许对象从机密降到内部级，但必须经过指定审核转换。写供Go策略宿主调用的路径验证包，标签构成有限偏序，转换边含主体与前置证据，普通操作只允许不降级。求给定事件轨迹中首个无合法证明的降级并返回缺失条件；共享证据有消费次数限制，用小状态枚举核对重复使用。

## 用法

```go
poset, _ := netsec.NewPoset(
    []string{"public", "internal", "confidential"},
    []netsec.PosetEdge{
        {Lower: "public", Upper: "internal"},
        {Lower: "internal", Upper: "confidential"},
    },
)

validator, _ := netsec.NewValidator(
    poset,
    []netsec.EvidenceSpec{{ID: "ticket", Capacity: 1}},
    []netsec.Transition{{
        Name: "review-c2i", Subject: "auditor",
        From: "confidential", To: "internal",
        Requires: []string{"ticket"},
    }},
)

violation, proof := validator.Verify([]netsec.Event{
    {Subject: "auditor", Object: "doc", From: "confidential", To: "internal"},
    {Subject: "auditor", Object: "doc", From: "confidential", To: "internal"}, // 证据已耗尽
})
// violation.EventIndex == 1，violation.Reason == netsec.ReasonEvidenceExhausted
```

## 语义

- 标签构成有限偏序，构造时计算传递闭包并拒绝成环的边。
- 普通操作（标签不变或升级）不消费证据；互不可比标签之间的流动直接拒绝。
- 严格降级只能走主体匹配、前置证据均有剩余次数的审核转换边。
- 证据为跨事件共享资源，按剩余次数向量做小状态枚举，状态去重后展开；
  任一可达状态走通即合法，否则报告首个违规事件与全部缺失条件。
- `Verify` 返回的 `Violation` 实现 `error`，可直接作为策略宿主的拒绝原因。
