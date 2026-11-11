# 攻击阶段关联唯一消费

告警引擎把同一登录事件匹配给多条攻击链，造成重复升级。写供Go检测宿主调用的关联包，输入时间有序事件、阶段模板和允许间隔，每个事件最多属于一条已确认链；在给定小窗口内选择完整链数量最多的分配，再按总跨度最小及事件ID排序决胜。最多二十四事件，预算耗尽返回已验证可行分配而不冒称最优。用全集枚举验证共享前缀与互相争用中间事件。

## 用法

入口是 `netsec.Assign`，零依赖、可被检测宿主直接调用：

```go
result, err := netsec.Assign(netsec.Request{
    Events: []netsec.Event{
        {ID: "e1", Stage: "login", Time: 0},
        {ID: "e2", Stage: "exploit", Time: 10},
        {ID: "e3", Stage: "exfil", Time: 20},
    },
    Templates: []netsec.ChainTemplate{
        {Name: "intrusion", Stages: []string{"login", "exploit", "exfil"}},
    },
    MaxGap: 30, // 相邻阶段允许的最大时间间隔
})
```

返回的 `Assignment` 中各链事件互斥（每个事件最多被一条已确认链消费），
且每条链都完整匹配模板并满足 `MaxGap`、可选 `MaxSpan` 小窗口约束。

## 决胜与预算语义

1. 最大化完整链数量。
2. 数量相同时最小化总跨度（各链首尾时间差之和）。
3. 仍相同时按链消费的事件 ID 元组做字典序决胜，保证结果确定。

`WorkBudget`（默认 `DefaultWorkBudget`）限制枚举步数，`MaxDuration` 限制墙钟时间。
预算耗尽时返回贪心种子保证的、已独立校验可行的分配，同时 `ProvenOptimal` 与
`Complete` 置为 `false` 并在 `Reason` 说明原因；只有完整跑完分支定界搜索才会
声明最优。输入事件最多 `MaxEvents`（24）个，越界或非法输入返回包装了
`ErrInvalidInput` 的错误。

## 验证

`oracle_test.go` 用独立代码路径实现全集枚举：单独的递归匹配生成全部候选链，
再对候选链做完整幂集枚举、按掩码拒绝事件重叠子集，得到参照最优解。生产实现
在共享前缀、中间事件争用、间隔/窗口边界以及 300 组随机小实例上与该 oracle
逐字段比对；24 事件边界与预算耗尽路径另有专项测试。
