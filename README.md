软件供应链门禁须防范可选依赖通过构建脚本或共享可写缓存向发布阶段偷渡临时凭据，给 Go 构建宿主写可导入的能力隔离验收包。输入为旧、新依赖图，每条边带平台条件、构建阶段（`fetch`/`preinstall`/`build`/`postinstall`/`publish`）、可选依赖标记、缓存键 `cacheKey` 与内容摘要。安装脚本（`preinstall`/`postinstall`）若未声明显式沙箱隔离边界，则继承并向后传递其触发者的临时凭据能力；若两节点共享可写 `cacheKey`，前序阶段写入缓存的污点可跨边污染后续读取同一键的 `publish` 实例。即使两条独立可选依赖分支各自合规，当二者在合法特征与平台组合下共同统一为同一高权限 `(pkg, digest, phase)` 时，仍判定权限联合越界；不同签名事务标识的半份凭据禁止拼接。计算新图相对旧图在合法平台条件与特征组合下新增的高权限实例集合，输出互不重叠的极小素蕴含条件块与可独立重放的授予链（无新增也给覆盖证明）。用小平台特征穷举对照构建脚本传染、缓存键投毒、双可选分支联合提权与条件环收敛。

## 导入包

无第三方依赖，Go 1.22+：

```go
import "formal.local/h26-25236a386a/capgate"
```

入口：

```go
report, err := capgate.Compare(oldGraph, newGraph, policy)
if err != nil { return err }
if err := report.VerifyCoverage(); err != nil { return err }
if !report.Pass() {
    // report.Blocks：互不重叠条件块
    // report.Chains：可独立 Replay 的授予/污染链
    return report.GateError()
}
```

## 输入模型

`capgate.Graph` 的每条 `capgate.Edge` 可携带：

- 平台布尔条件 `condition`，例如 `linux && (arm64 || amd64) && !experimental`；
- 阶段 `phase`：`fetch`、`preinstall`、`build`、`postinstall`、`publish`；
- `optional: true` 可选依赖/分支标记；
- `cacheKey`、`cacheMode`（省略模式但有 `cacheKey` 时按共享可写的 `read-write` 处理）和 `digest`；
- `sandboxed: true` 表示安装脚本声明显式沙箱边界；
- `grants` 表示临时凭据、能力、签名事务 ID 或半份凭据。

平台特征由 `Policy.Features` 限定；`Policy.LegalCondition` 可排除不可能组合，例如用 `capgate.OneHot("linux", "darwin")` 约束操作系统互斥。包会穷举所有合法组合，默认上限 12 个布尔特征。

## 判定语义

- **脚本传染**：未设置 `sandboxed: true` 的 `preinstall/postinstall` 边会继承 `From` 节点的能力并传播到 `To`；沙箱边阻断环境能力穿越，但保留显式授予该边的凭据。
- **缓存投毒**：同一 `cacheKey` 上，前序阶段的跨边写到达后续 `publish` 读实例时生成 `cache-key-poisoning` 链。
- **双可选分支统一**：两个独立可选分支到达同一个 `(pkg, digest, phase)`，且单独均不触线时，默认判定 `dual-optional-union-escalation`。也可用 `Policy.HighPrivilegeUnions` 精确声明能力组合。
- **半份凭据**：仅当 `half.family`、序号集合完整且 `transactionId` 完全相同时才合成 `credential:<family>`；不同事务 ID 不会拼接。
- **条件环**：能力和分支集合使用单调不动点传播，条件环会收敛而不是递归失控；最短无环路径用于生成重放链。

## 条件块与证明

对每个新增的 `(pkg, digest, phase)`，报告包含：

- `primeImplicants`：通过 `3^n` 立方体穷举得到的精确素蕴含；
- `blocks`：两两原始立方体不相交的覆盖块，恰好覆盖全部“新图高权限、旧图同组合不高权限”的合法赋值；
- `blocks[].primeExpr/primeCube`：包含该不相交余项块的原始素蕴含；
- `chains`：内嵌边、赋值、凭据/事务、缓存跳转和分支路径，可调用 `GrantChain.Replay()` 独立复验；
- `coverage`：全部合法赋值上的旧/新高权限实例清单及 SHA-256 摘要。无新增时 `coverage.noAdded=true`。

数学上，任意布尔函数不一定存在“两两严格不相交且每块都是原函数素蕴含”的覆盖（例如三变量多数函数的三个素蕴含两两相交）。因此本包同时给出精确素蕴含和保证不相交的余项素块；每个输出块相对于已覆盖区域都是包含极小的素块，`VerifyCoverage` 会重算并检查精确覆盖、原始立方体零重叠、素蕴含集合和所有链。

## CLI

```bash
go run ./cmd/capgate \
  -old old.json \
  -new examples/new-graph.json \
  -features linux,arm64
```

门禁失败时输出 JSON 报告，二进制退出码为 `2`。

## 验证

```bash
go test ./...
go vet ./...
go test ./capgate -cover
```

测试用小型特征空间穷举对照了脚本传染、沙箱阻断、缓存键投毒、双可选分支、跨事务半份凭据隔离、旧图条件扩张、JSON 往返和条件环收敛。
