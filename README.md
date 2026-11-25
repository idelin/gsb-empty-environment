# upgradeplan

`prescreen.local/ps-75bdec7ff4` 的根包名是 `upgradeplan`，可供 Go 安装宿主直接导入。它在“服务器只能看到本轮成功数量、看不到具体设备”的部分可观测条件下，穷举初始情况、公钥别名、SDK 匹配、旧签名门槛和设备自选新签名集合，求最短最坏轮数的广播决策树。

## 建模口径

- `DeviceState`：包名、当前签名者集合、设备 SDK（或可选 SDK 区间）、当前数据标记，以及内部延续用的 `InitialData` / `DataLost`。
- `Release`：已验签发布记录，包含 SDK 区间、旧签名门槛、授权旧签名者、一个或多个新签名集合，以及是否授权携带已有数据。
- 门槛按不同公钥材料计数：`AliasSigner(id, key)` 中多个 ID 共享同一公钥时只算一票；新签名集合中的别名在后续门槛中同样去重。
- 广播 `Broadcast{ReleaseID}` 只指定发布包。包名、SDK 区间和旧签名门槛都匹配的设备全部升级，其余设备状态不变。
- 有多个 `NewSignerSets` 时，每台符合条件的设备自选一个；规划器穷举这些选择，并只按 `SuccessCount` 合并运行时分支。
- `CarryExistingData: false` 的步骤作用于初始有数据的设备时会设置粘性 `DataLost`。即使后续升级到受信集合，验收仍会拒绝该轨迹。
- 设备和初始舰队都按规范键排序/去重；交换设备顺序、世界顺序不会产生不同的计数分支或发布器决策。

硬限制为 `MaxDevices=8`、`MaxReleases=12`、`MaxRounds=8`。同一 `Release.ID` 不会被加入“已使用”集合，因此可以重发。

## 快速使用

```go
import upgradeplan "prescreen.local/ps-75bdec7ff4"

trusted := upgradeplan.KeySignerSet("TRUSTED")
problem := upgradeplan.Problem{
    InitialFleets: [][]upgradeplan.DeviceState{
        {{SDK: 24, Signers: upgradeplan.KeySignerSet("OLD"), HasData: true}},
        {{SDK: 30, Signers: upgradeplan.KeySignerSet("OLD"), HasData: true}},
    },
    Releases: []upgradeplan.Release{
        {
            ID: "r-probe",
            SDKRange: upgradeplan.SDKRange{MinSDK: 28, MaxSDK: 32},
            OldThreshold: 1,
            NewSigners: upgradeplan.KeySignerSet("PROBED"),
            CarryExistingData: true,
        },
        // count=0 分支的低 SDK 发布包、count=1 分支的高 SDK 发布包...
    },
    Target: upgradeplan.Target{Trusted: trusted},
}

plan, err := upgradeplan.Solve(problem)
if err != nil {
    var infeasible *upgradeplan.InfeasibleError
    if errors.As(err, &infeasible) {
        // infeasible.Failure.Candidates 覆盖每个候选广播及失败计数分支/反例。
    }
    return err
}

report, err := upgradeplan.VerifyPlan(problem, plan) // 独立穷举验收
if err != nil || !report.Accepted {
    // report.Violations 含拒绝原因和完整 Trajectory。
}

session, _ := upgradeplan.NewSession(plan)
broadcast, _ := session.Current()       // 本轮唯一广播
next, err := session.Observe(0)         // 服务器只见成功数 0
```

`Plan.TreeText()` 输出完整计数决策树，例如：

```text
worst_rounds=2
broadcast r-probe
  count=0 ->
    broadcast r-low
      count=1 ->
        terminal
  count=1 ->
    broadcast r-high
      count=1 ->
        terminal
```

## 主要 API

- `Solve(Problem) (*Plan, error)`：最短最坏轮数与每个可见计数分支的下一条广播。
- `VerifyPlan(Problem, *Plan) (*VerificationReport, error)`：独立重放所有初始舰队、每一步的全部选择组合和计数分支。
- `Accept(Problem, *Plan)` / `AcceptPlan(...)`：验收别名，失败时返回含报告的 `*AcceptanceError`。
- `ReplayTrajectory(...)`：按已验签记录重放指定广播/选择，并独立推导粘性数据丢失。
- `AcceptTrajectory(Trajectory, Target)`：检查单条已解析轨迹；任何一步出现粘性数据丢失或初始数据标记消失都会拒绝。
- `AcceptFinalState([]DeviceState, Target)`：即使终态签名集合受信，也拒绝丢过数据的终态。
- `OldKeyVoteCount(...)`、`EligibleIndices(...)`、`EnumerateOutcomes(...)`：可单独复用的语义原语。
- `NewSession(*Plan)`：宿主运行时游标，只接收成功计数。
- `AllMultisetFleets(...)` / `AllCartesianFleets(...)`：从有限安装状态宇宙生成所有初始设备群。

不可行时，`errors.As(err, &infeasible)` 得到的 `Failure.Candidates` 按发布包 ID 排序，包含每个候选广播的所有失败计数分支、规范信念键、递归失败依据和具体 `Witness` 舰队。

## 测试对照

`go test -count=1 ./...` 覆盖：

- 公钥别名在旧门槛和新集合中均只投一票。
- 精确的小设备群完整计数决策树文本对照。
- 同一成功计数下的 `UU` / `UV` / `VV` 异签名世界由同一条广播处理。
- 设备顺序、初始世界顺序不改变计划 JSON 或决策树。
- 中途 `r-wipe` 丢数据后再升级到受信集合仍被验收拒绝。
- 不可行问题中每个候选广播都有结构化失败依据。
- 8 台设备、12 个发布包、8 轮链式选择的规模冒烟。

## 需求原文

设备群里一部分安装包曾从应急签名分支升级，不能仅按厂商宣称的最新证书认定所有设备都可迁移。请实现 Go 安装宿主可导入的升级计划包。输入为有限安装状态（包名、签名者集合、设备 SDK 区间、已有数据标记）与已验签的升级记录。记录的旧签名者门槛按不同公钥材料计数，新集合中同公钥的两个 ID 只算一票；沿升级链沿用已有数据须每一步都有相应授权且不得出现中途丢失初始已有数据。一条广播指令只指定发布包，匹配其 SDK 区间且满足旧签名门槛的设备升级，其余设备原地等待。某些升级允许多个新签名集合，由设备择一，服务器只见本轮成功数量，看不到是哪几台。至多 8 台、12 个发布包和 8 轮；同一包可重发。判断是否存在依赖这些可见计数的发布方案，使所有初始情况与所有设备选择都保住已有数据，最终都进入指定受信签名集合。给出最短最坏轮数、各计数分支下一条广播及独立验收函数；验收函数必须拒绝任何中途丢失过已有数据的终态或轨迹，不可行须覆盖所有候选广播的失败依据。计数相同的真实状态不能因顺序不同而当成发布器可区分。用小设备群完整决策树对照，覆盖门槛别名、中途丢数据后再升级和同计数不同签名设备。
