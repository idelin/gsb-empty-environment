多级交叉签名 PKI 信任网络存在命名限制、最大路径长度约束与环状互签结构时，管理员撤回一条中继委托后若按路径字符串或逐维取并集计算剩余授权，极易把分别来自两条交叉父链的允许时段与允许动作错拼成从未被任何单条完整链授权过的越权组合。为Go认证宿主实现可导入的图状交叉签发委托裁剪与三维切片校验包：支持至多2000个通过共享子链形成有向图（允许无根有向环与菱形交叉互签）的证书节点，绝对禁止指数级枚举从根到叶的全部路径。每条委托边的权限由三维精确乘积限制：(a) 结构化作用域前缀（含允许前缀集与不可拼接的 `excludedSubtrees` 排除例外，如域名后缀或 URI 前缀）、(b) 有限动作集合、`maxPathLen` 深度预算，以及 (c) `[NotBefore, NotAfter)` 有效区间；子授权只有在存在至少一条无环可信根链上同时满足逐跳 `maxPathLen` 递减、每个维度均落入同链交集而不踩中沿途任一排除例外时方为合法。撤销操作采用基于签名摘要冻结的宿主原子比较并替换（CAS）批处理，撤销某条委托后必须清除仅靠无根环或已断链支撑的权限，同时保留仍具独立合法根链支撑的三维切片；输出每张受影响证书的保留三维不相交切片列表与失去支持切片证书，附不依赖图缩减的独立根链语义验收器及小图逐路径穷举测试（含深度耗尽、环路隔离与排除例外撕裂）。

## crossmesh 包

导入路径 `formal.local/w3b-c0c84dab1e/crossmesh`。

核心设计：

- 作用域按所有注册前缀切分为原子单元（trie 单元），时间线按各边端点切分为原子半开区间，动作压成 64 位掩码；一个体素即 `(作用域单元, 时间区间, 动作位)`。
- 求解器对每个 `(单元, 时间)` 列独立做带剩余深度预算的工作清单不动点传播，状态键为 `(节点, 剩余预算)`。列与列完全隔离，动作位只沿在该列放行的边传播，因此不同父链的时段与动作不可能被错拼；无根环上的权限永远到不了可信根种子。
- `maxPathLen` 采用 X.509 `pathLenConstraint` 语义：跨边时新预算为 `min(到达预算-1, 该边 MaxPathLen)`，零预算边只落地终端授权、不再续发。
- 撤销为基于边集合 SHA-256 摘要冻结的原子 CAS 批处理；摘要不匹配时引擎原样保留，匹配后整体重算并输出每张受影响证书的保留/失去不相交切片及完全失去支持的证书。
- `IndependentAcceptor` 是不共享求解器代码的独立语义验收器，仅在 ≤12 节点的小图上逐简单链穷举，用于交叉校验，绝不用于大图。

最小用法：

```go
eng, err := crossmesh.New(crossmesh.Graph{
    Nodes:        []string{"root", "ca", "leaf"},
    TrustedRoots: []string{"root"},
    Edges: []crossmesh.DelegationEdge{
        {
            ID: "root-ca", Issuer: "root", Subject: "ca",
            AllowedPrefixes: []crossmesh.Name{{Kind: crossmesh.KindDomain, Value: "example.com"}},
            ExcludedSubtrees: []crossmesh.Name{{Kind: crossmesh.KindDomain, Value: "secret.example.com"}},
            Actions: []string{"sign"}, MaxPathLen: 2,
            NotBefore: 0, NotAfter: 3600,
        },
        // ...
    },
})
if err != nil { /* ... */ }

ok, _ := eng.IsAuthorized("leaf",
    crossmesh.Name{Kind: crossmesh.KindDomain, Value: "www.example.com"},
    "sign", 1200)
slices, _ := eng.Slices("leaf")

report, err := eng.Revoke([]crossmesh.Revocation{
    {EdgeID: "root-ca", ExpectedDigest: eng.Digest()},
})
_ = report.Applied // 摘要冲突时为 false，引擎不被修改
```

验证：`go test ./...`（含 2000 节点规模、深度耗尽、环路隔离、排除撕裂、菱形防错拼、CAS 冲突与 200 组随机小图的独立验收器等价性测试）。
