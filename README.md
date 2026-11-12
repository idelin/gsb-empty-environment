# 构建器可能优先选择公共镜像中与内部包同名的高版本包

构建器可能优先选择公共镜像中与内部包同名的高版本包。为Go依赖准入宿主实现解析验证包，每条依赖边声明受信源身份、版本闭区间和允许签名者，候选含源、版本、摘要及由注入验签器核验的证明；所有实际可达节点必须符合各入边约束，不能按包名合并不同来源。支持至多12个布尔平台条件，输出能让未授权候选入选的最小配置与依赖路径；独立枚举配置并测试同名高版本、来源别名及循环依赖。

## 实现

包 `taskpkg`（模块 `example.org/domainreissue/q005`）实现准入感知的依赖解析，
不依赖任何第三方库。

- `types.go`：`NodeID` 以 `(Source, Path)` 二元组标识节点，同源同路径才算同一节点；
  `Candidate` 携带源、语义版本、摘要与不透明 `Proof`；`Verifier` 接口由宿主注入，
  包内提供测试/离线用的 `MapVerifier` 与 `VerifierFunc`。
- `graph.go`：每条 `Edge` 内联 `Constraint`，声明唯一受信 `SourceID`、闭区间
  `[MinVersion, MaxVersion]`、允许签名者集合，以及可选平台 `Condition`。校验阶段强制
  边约束的源与目标节点源一致，从结构上杜绝按包名合并不同来源。
- `semver.go`：语义化版本解析与比较（含预发布优先级、构建元数据忽略），
  `InClosedInterval` 提供含两端边界的区间判定。
- `resolve.go`：两阶段解析。阶段 A 沿当前平台下的活跃边 DFS，收集每个可达节点的
  全部入边并检测环（默认拒绝，`AllowCycles` 时记录）；阶段 B 对每个节点取满足
  所有入边约束交集、且通过注入验签器的最高版本候选。错误以 `ErrUnsatisfiable`、
  `ErrVerification`、`ErrCycle`、`ErrInvalidInput` 哨兵包装，支持 `errors.Is`。
- `platform.go`：固定上限 `MaxPlatformConditions = 12` 的布尔位掩码 `PlatformConfig`，
  以及 `Bool/Not/And/Or/Always/Never` 条件表达式；`Configs(bits)` 独立生成 2^bits 个配置。
- `policy.go`：宿主级基线 `Policy`（受信源与受信签名者），用于区分“边允许但宿主
  未授权”的候选并给出 `ReasonUntrustedSource`/`ReasonUntrustedSigner`。
- `audit.go`：`Enumerate` 对每个平台配置独立运行一次无共享状态的解析，收集未授权
  入选 `Finding`；按启用位数（再按掩码、路径长度）排序，`Report.Minimal` 给出让未授权
  候选入选的最小配置，`Finding.Path` 用 BFS 给出最短根到目标依赖路径。

## 测试

`go test ./...` 覆盖：同名公共高版本（依赖混淆）与最小配置审计、来源别名隔离、
循环依赖（默认拒绝/允许记录/自环）、平台门控下的独立配置枚举、闭区间边界、
签名者回退、注入验签器拒绝伪造证明、多入边约束交集与冲突、12 位条件上限。
