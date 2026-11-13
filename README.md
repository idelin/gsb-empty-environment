# 相邻行双面翻转的物理布局审查与重排

Go 包 `netsec` 实现本题的物理布局审计、最小迁移求解和在线迁移顺序验证。
物理页号固定为 `0..4095`，由调用方的纯函数 `Layout.Locate` 解码为
`(channel,bank,row)`；成功解码的坐标必须落在 `Layout.Limits` 内。

```go
layout := netsec.Layout{
    Pages: []netsec.Page{
        {ID: 1, Tenant: "db", Role: netsec.Sensitive, Physical: 40},
        {ID: 2, Tenant: "untrusted-a", Role: netsec.Attacker, Physical: 41, Movable: true},
        {ID: 3, Tenant: "untrusted-b", Role: netsec.Attacker, Physical: 42},
    },
    FreePages: []int{1000, 1001},
    Limits:    netsec.Limits{Channels: 2, Banks: 8, Rows: 64},
    Locate: func(physical int) (netsec.Location, bool) {
        // 宿主自己的纯地址解码函数；false 表示未映射。
        return decode(physical)
    },
}

report, err := netsec.Audit(layout)
result, err := netsec.Repair(layout, &netsec.RepairOptions{Budget: 1_000_000})
```

## 审计模型

见证是满足下列条件的**三页**三元组：

- 同一 `channel` 与 `bank`；
- 敏感页位于第 `row` 行；
- 两条不同 attacker 页分别位于 `row-1` 与 `row+1`；
- 三页均成功映射；tenant 名称不限制 attacker 资格。

`Audit` 返回全部见证，而不是只返回一个样本。同一中心或同一侧存在多页时，
按完整笛卡尔积展开。结果按
`(Sensitive, AttackerBelow, AttackerAbove)` 的页 ID 排序。行 `0` 没有
`row-1`，最后一行没有 `row+1`，因此边界行不会产生越界见证。

## 修复与排序

- 只有 `Movable` 的 attacker/sensitive 页可迁移；ordinary 页不参与见证，忽略其迁移选项。
- 目标只能是 `FreePages`，一一占用；页 ID 与内容身份保持不变。
- 先最小化被迁移页数，再按“迁移页 ID 升序列表、对应目标物理页升序列表”
  取字典序最小方案。
- 初始见证集合非空时，修复后的最终见证集合必须为空。
- 同几何位置的空转不会改变邻接关系，因此不作为有效候选迁移。
- 与初始见证无关但可迁移的 attacker/sensitive 页也可能被新见证“牵连”，
  所以搜索候选覆盖所有此类可迁移页，而不只覆盖初始见证参与者。

`RepairResult.Moves` 是规范排序后的最终分配；`Sequence` 是实际可执行的
在线顺序；`Counts` 给出初始及每一步之后的见证数。

## 在线顺序

这里采用“不得增加见证集合”的严格集合解释：每一步之后的三元组集合必须是
上一步集合的子集。移动一页到不同几何位置时，唯一可能新增的见证都包含该页，
因此只需检查该页在目标位置是否成为见证参与者。这个约束比“见证数量不增加”
更强；例如敏感页先移到目标会产生新三元组时，即使祖先见证同时消失、数量不变，
该顺序仍会被拒绝，可能要求先搬走阻挡的 attacker 页。

若静态安全方案存在但找不到任何严格在线顺序，`Repair` 返回
`ErrNoOnlineOrder` 并保留静态方案。也可用 `FindOnlineOrder` 对给定最终分配
单独求解顺序，或用 `ValidateOnlineOrder` 逐字校验已有顺序。

## 预算与边界

- 最多接受 20 个可迁移的 attacker/sensitive 页和 20 个空闲页。
- `RepairOptions.Budget` 限制搜索状态数；耗尽时返回
  `ErrBudgetExhausted`、已找到的最佳静态分配（若有）和尚未闭合的
  `LowerBound`。
- `ErrNoSafePlacement` 表示连静态无见证布局都不存在；
  `ErrMappingOutOfRange` 表示映射函数成功返回了越界坐标。

本包的结论仅针对本题的相邻行三页邻接模型，不声称模拟或预测真实 DRAM
扰动率。

## 验证

测试包含独立的全三元组参考枚举，以及小规模修复暴力参考：枚举所有留驻/
空闲页分配、全部迁移排列并重建见证集合，核对银行交错、边界行、同侧多页、
不可迁移敏感页、非初始见证 attacker 被迫先迁移等情形。

```sh
go test ./...
go test -race ./...
go vet ./...
```
