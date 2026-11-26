即使每条 BGP 路径都严格执行了 AS_PATH 防环，跨自治系统的本地策略偏好冲突依然会引发永久性路由震荡（经典 Bad Gadget / Griffin Stable Paths Problem），甚至在看似满足 Gao-Rexford 商业无谷模型（客户 > 对等 > 供应商）的网络中，只要少数备份链路或跨运营商本地偏好打破了单调性，就会形成隐藏的争议轮（Dispute Wheel）导致收敛失败或多稳态分裂。为域间路由分析控制面交付可导入的 Go BGP 策略稳定性与争议轮检测包。输入为面向目标前缀 `d` 的 AS 图，每个 AS 配置了其对全部允许简单路径（按 `localPref` 与策略排序、禁止环路、支持 Gao-Rexford 导出过滤规则——即不向非客户转运来自对等或供应商的路由！）的严格偏好序。组件实现：1）导出过滤与合法候选路径展开，核验是否满足严格 Gao-Rexford 无谷及无商业环（无客户-供应商环）；2）精确求解所有稳定路径分配（Stable Path Assignments：每个 AS 选中的路径均为其所有邻居当前通告的合法可扩展路径中偏好最高者！），区分三类结局：唯一稳态（安全收敛）、多稳态（依赖消息时序的非确定性分裂，须输出至少两个互斥稳态！）以及无稳态（必然永久震荡！）；3）对存在振荡或多稳态的实例，构造并输出具体的争议轮结构（由 Pivot 自治系统环、各自直接路径 `Q_i` 与经邻居扩展的更优路径 `R_i` 组成，且证明每步满足 `R_i >_i Q_i` 形成循环偏好！）；4）计算最小策略调整集合（修改最少条本地偏好排名或禁用最少条非核心导出边）消除全部争议轮并验证修复后存在唯一稳态。验收覆盖经典 3-AS Bad Gadget 无稳态检测、4-AS Disagree 双稳态输出、标准无谷网络唯一稳态证明、违规备份链路打破无谷触发争议轮以及最小修复后唯一稳态验证。

---

## 交付物

- `pkg/spp/`：可导入的稳定路径问题（SPP）分析库，纯标准库、零外部依赖。
- `cmd/bgpstable/`：控制面 CLI，输出中文文本报告或机器可读 JSON。
- `testdata/`：五个验收实例夹具。
- 测试：`pkg/spp/*_test.go`，含与独立暴力枚举器的完备性交叉验证。

## 理论模型

实例是面向单一目标前缀 d 的 Griffin-Wilfong 稳定路径问题（SPP）：

- 每个 AS 给出对全部允许简单路径的严格全序偏好（数组下标越小，
  localPref 越高）；路径必须以自身开头、以 destination 结尾。
- 边的有向类型：c2p（客户→供应商，上行）、p2c（供应商→客户，下行）、
  p2p（对等）。
- Gao-Rexford 无谷规则：合法路径形如 up* (peer)? down*，等价于导出
  过滤“来自对等体/供应商的路由只能通告给客户”。ValleyFreeLegalPaths
  用状态机枚举全部合法简单路径；任何已配置但不在其中的路径都会被标记为
  无谷/导出违规并给出具体违规跳；同时在 c2p 子图上检测客户-供应商商业环。

稳定路径分配（SPA）要求：每个非源站 AS 选中的路径，恰好是其所有邻居在
当前分配下通告的合法可扩展路径中偏好最高者；无可用路由时选空。求解器
采用“分支决策 + 安全约束传播”的精确枚举，并以独立全空间暴力枚举交叉
验证完备性。三类结局：

- unique_stable：唯一稳态，安全收敛；
- multiple_stable：多稳态，依赖消息时序的非确定性分裂（输出至少两个
  互斥稳态）；
- no_stable：无稳态，必然永久震荡。

争议轮（Dispute Wheel）为 (U, Q, R)：pivot 环 U=(u_0,…,u_{k-1})，
直接路径（辐条）Q_i 不经过其他 pivot，轮缘路径 R_i 以环上邻居
u_{i+1} 开头，且逐步证明 R_i 优于 Q_i 构成循环偏好。

最小修复从两类原子动作中按子集尺寸自底向上搜索：禁用一条本地偏好排名
条目（preference_edit）或禁用一条非核心导出边（export_edge_disable，
仅对等边与制造“谷”的备份链路；核心 c2p 供应商链路不可禁用）。命中条件：
修复后争议轮清零且恰有唯一稳态；require-valley-free 进一步要求恢复
严格无谷合规。

## 输入格式（JSON）

    {
      "destination": 0,
      "nodes": [0, 1, 2],
      "edges": [
        {"from": 1, "to": 0, "kind": "c2p"},
        {"from": 2, "to": 1, "kind": "c2p"}
      ],
      "preferences": {
        "0": [[0]],
        "1": [[1, 0]],
        "2": [[2, 1, 0]]
      }
    }

## CLI

    go run ./cmd/bgpstable testdata/bad_gadget.json          # 中文文本报告
    go run ./cmd/bgpstable -json testdata/disagree.json      # JSON 报告
    go run ./cmd/bgpstable -require-valley-free -max-repair 2 \
      testdata/backup_violation.json                          # 强制恢复无谷

## 作为库导入

    cfg, _ := spp.LoadConfigFile("testdata/disagree.json")
    report, err := spp.Analyze(cfg, spp.AnalysisOptions{
        WheelLimit: spp.DefaultWheelLimit,
        Repair:     spp.RepairOptions{MaxSize: spp.DefaultMaxRepairSize},
    })
    fmt.Println(report.Outcome, report.HasDisputeWheel)

## 验收夹具

| 夹具 | 预期结局 | 说明 |
| --- | --- | --- |
| bad_gadget.json | 无稳态 | 经典 3-pivot Bad Gadget，永久震荡 |
| disagree.json | 多稳态 | 4-AS Disagree，两个互斥稳态 |
| clean_valley_free.json | 唯一稳态 | 严格无谷、无争议轮 |
| export_filter.json | 唯一稳态 | 无谷展开等价于 GR 导出过滤 |
| backup_violation.json | 多稳态 | 违规备份链路触发争议轮，最小修复后唯一稳态 |

## 测试

    go test ./...          # 全部验收与单元测试
    go test -race ./...    # 竞态检测
