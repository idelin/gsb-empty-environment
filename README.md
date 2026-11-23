发布系统将收集到的批准凭据交给离线机房，审核人只能提前选定一次签署者与委托链，机房到发布时才知道各签署设备的确切校时结果。给 Go 发布宿主增加可导入的授权求解包，保持已有 clockwire.Bound 的有符号字节格式。设备本地时间为真实整数时刻加固定偏移；校时证据只给出若干设备间偏移差的上下界，一个设备的偏移在整次检查中不能各证书分开选择，参考设备偏移为零。每张真实 Ed25519 凭据绑定制品摘要、角色、设备与本地半开有效区间。策略是一棵阈值树，同一永久身份整树只能用一次，且跨委托链角色降级不得重复计票；候选签署者最多12人。发布日另有两个互不相交的真实时间窗口。要选出一份固定见证，使每一种符合校时证据的偏移组合下，都至少存在一个共同发布时间落在允许窗口，并让该见证所有凭据同时有效。不能为不同偏移分别换一组签署者。签署者最少优先，并列按永久 ID 序列取最小。值域可达10的12次方，不得逐时刻或逐偏移遍历；返回可由独立函数检查的见证和时间覆盖证明。校时证据矛盾、见证不鲁棒、原始签名错误与有符号边界溢出要分别报告。小范围穷举对照共享偏移、端点恰接、两窗交替承担发布及每次校时都能选人但无固定见证的情形。

## 包结构

- `clockwire`：`Bound` 及 16 字节大端二进制补码的有符号字节格式。
- `authsolve`：求解、验证与时间覆盖证明。模块根目录以及 `authorization`、`authz`、`releaseauth` 均提供同一套别名导出，便于现有发布宿主选择导入路径。

## 快速使用

```go
problem := authsolve.Problem{
    Artifact: digest,
    Devices: []authsolve.Device{
        {ID: "ref", Reference: true, PublicKey: refPub},
        {ID: "hsm-1", PublicKey: hsmPub},
    },
    Credentials: []authsolve.Credential{{
        ID: "c-1", Identity: "alice", Device: "hsm-1", Role: "release",
        Artifact: digest, NotBefore: nb, NotAfter: na,
    }},
    Policy: authsolve.PolicyNode{
        Threshold: 1,
        Children: []authsolve.PolicyNode{{Role: "release"}},
    },
    Windows: []authsolve.Window{{Start: w1s, End: w1e}, {Start: w2s, End: w2e}},
    Clock: []authsolve.ClockConstraint{{
        Device: "hsm-1", Other: "ref", Bound: clockwire.New(-3, 3),
    }},
    ReferenceDevice: "ref",
    RoleLevels: map[string]int{"release": 2, "viewer": 1},
}

w, err := authsolve.Solve(problem)
if err == nil {
    err = authsolve.Verify(problem, w)       // 完整独立复核
    err = authsolve.VerifyProof(problem, w.Proof) // 只复核时间覆盖证明
}
```

`Solve` 按签署者人数递增搜索，找到最少人数后仍会比较该人数下所有候选，按排序后的永久 ID 序列取最小；再以凭据 ID 序列作确定性次级排序。最多枚举 12 个候选签署者。

## 时间鲁棒性

设所选凭据在设备 `d` 上合并后的本地区间为 `[NB_d, NA_d)`，设备偏移为 `o_d`，参考设备 `o_ref = 0`。共同发布时间集合为

```text
J(o) = [ max_d(NB_d - o_d), min_d(NA_d - 1 - o_d) ].
```

`J(o)` 与两个允许窗口的并集不相交，只能属于以下四类之一：整体在左、落在两窗之间、整体在右、区间为空。每一类都是偏移差约束的有限并集；对每个原子用 Bellman-Ford 判断“校时证据 + 失败条件”是否可满足，即为精确判定。判定不枚举时刻或偏移，只与设备数和约束数有关。

对每个不可满足的失败原子，证明保存一条负权环。负权环的边权和为负，直接给出该原子不可满足的 Farkas 证书；`VerifyProof` 重建同一差分约束系统并逐边核验环与权重，因此不依赖求解器的内部状态。

## 错误分类

- `ErrClockInconsistent`：校时证据存在负环或空边界。
- `ErrWitnessNotRobust`：给定固定见证在某个允许偏移下无法发布。
- `ErrNoWitness`：每个校时配置都能选人，但不存在一份固定见证。
- `ErrSignature`：存在的原始 Ed25519 签名无法验证。
- `ErrSignedOverflow`：有符号 64 位边界运算溢出。

## 测试

`go test ./...` 包含小范围穷举对照：共享设备偏移、端点恰接窗口、两个窗口分别承担不同偏移下的发布、每个校时配置可选但无固定见证，以及随机小实例上与逐偏移/逐时刻穷举的结果比对。
