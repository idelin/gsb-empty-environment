分布式对象存储的 CRUSH 算法通过分层权重树把归置组（PG）映射到不同故障域的 OSD，但权重归零、设备下线或某机架下所有磁盘不可用时，冲突重试与 `chooseleaf` 上层回退的细节极易造成同机架双副本或映射抖动。为集群映射模块交付可导入的 Go CRUSH Straw2 实现包。输入层次拓扑树（节点含编号、类型如 root/rack/host/osd、静态权重、运行态 reweight 比例与 up/down 状态）、确定性 64 位整散列函数以及选盘规则步序（支持 `take`、`choose`、`chooseleaf`、`emit` 与 `firstn`/`indep` 两种重试模式及每步最大尝试次数）。规则：Straw2 在节点桶内对每个正权重子节点用确定性哈希生成均匀抽签值并按 `ln(u)/weight` 取最大者；当选出已选过节点、或该节点为 down/权重 0、或 `chooseleaf` 下探到叶子无可用盘时按 `firstn`（递增局部重试序号）或 `indep`（保持槽位索引独立，失败留空位 `-1` 不平移后续副本）执行局部重试及外层回退。组件输出最终 OSD 列表及每槽是否发生冲突/回退统计。验收核对全部故障域约束、单 OSD 置 down 时 `indep` 模式其余槽位零漂移、整个机架全 down 时自动回退到其余机架凑齐副本数、零权重节点永不被选中，以及大规模 PG 分布占比与权重偏差不超过统计容差。

## Go 用法

根包名是 `crush`，也可从 `prescreen.local/ps-7261f1735c/crush`、`.../straw2` 或 `.../pkg/crush` 导入同一组 API。

```go
package main

import (
    "fmt"

    crush "prescreen.local/ps-7261f1735c"
)

func main() {
    root := crush.NewNode(0, "root", 64)
    for r := 0; r < 4; r++ {
        rack := crush.NewNode(100+r, "rack", 16)
        root.Children = append(root.Children, rack)
        for h := 0; h < 2; h++ {
            host := crush.NewNode(200+r*2+h, "host", 8)
            rack.Children = append(rack.Children, host)
            for d := 0; d < 2; d++ {
                id := 300 + r*4 + h*2 + d
                host.Children = append(host.Children, crush.NewOSD(id, 4))
            }
        }
    }

    rule := &crush.Rule{
        Name: "replicated-rack",
        Steps: []crush.Step{
            crush.TakeStep(0),
            crush.ChooseLeafStep(crush.Indep, 3, "rack", 64),
            crush.EmitStep(),
        },
    }

    mapper := crush.NewMapper(root, crush.SplitMix64, rule)
    result, err := mapper.MapPG(uint64(42), 3, "replicated-rack")
    if err != nil {
        panic(err)
    }

    fmt.Println(result.OSDs)   // [315 310 307]
    fmt.Println(result.Slots)  // 每槽冲突、down、重试、外层回退计数

    mapper.SetDown(315, true)
    after, _ := mapper.MapPG(uint64(42), 3, "replicated-rack")
    fmt.Println(after.OSDs)    // [312 310 307]
}
```

## 语义

- Straw2 对有效权重 `weight * reweight > 0` 的子节点计算确定性 `u ∈ (0,1)`，选择最大的 `ln(u)/weight`。
- down 是运行态状态，不直接抹掉抽签权重；因此可以复现“抽中 down 节点 → 记录诊断 → 重试/回退”的过程。
- `indep` 的哈希坐标固定包含槽位号；单个 OSD 下线只改变包含该 OSD 的槽位，其他槽位保持零漂移。尝试耗尽时该槽位为 `-1`，后续槽位不平移。
- `firstn` 使用递增选择序列，失败后跳过坏候选，后续结果按 CRUSH firstn 语义平移补齐。
- `chooseleaf ... type rack` 保证 OSD 叶子位于不同 rack；将 `type` 改为 `host` 时保证 host 故障域互斥。
- `Result.Slots`/`Result.Stats` 提供每槽 `Conflict`、`Fallback`、`OuterFallback`、`DownCount`、`RetryCount`、`Attempts` 等统计。

## 验证

```bash
gofmt -w .
go test -race -count=1 ./...
```

内置验收测试覆盖 5000 个 PG 的故障域约束、16 个 OSD 逐盘 down 后的 indep 零漂移、整机架 down 回退、零权重/零 reweight 永不命中，以及 30 万 PG 的静态权重和 reweight 分布容差。
