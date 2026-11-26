内核沙箱过滤表优化后若混淆不同架构调用号别名或放宽双参数关系约束，将造成沙箱逃逸，给 Go 沙箱宿主实现可导入的策略等价校验包。参考语义为兼容 `x86_64` 与 `ia32` 双架构的有序规则链，动作优先级从高到低为 `KILL > TRAP > ERRNO > TRACE > LOG > ALLOW`，同优先级取最近规则的数据载荷并保留沿途全部审计标签义务。规则谓词除架构、调用号和六个 64 位参数掩码与无符号区间外，支持双参数关系谓词（`arg[i] == arg[j]`、`(arg[i] & mask) <= arg[j]`）与调用方自定义命名谓词。优化产物为只能向前跳转的判定 DAG。组件须证明优化 DAG 在全部合法双架构输入空间下既不放宽动作、不丢失任何审计/通知义务，也不错乱调用号传递载荷，并在调用方指定透明区做到逐动作完全等价。建立密码学依赖溯源图：修改单个命名谓词仅重算受影响 DAG 分支，并拒绝伪造部分复用证书；失败返回不相交符号输入区及双方决定。提供小位宽全枚举参考，重点核对双架构号别名、双参关系跨越最高位、部分证书篡改与等优先级数据覆盖。

## Go 包用法

根目录就是可导入包 `policycheck`，无第三方依赖：

```go
import policycheck "formal.local/h26-c50a7102ec"
```

核心入口：

- `policycheck.Policy` / `policycheck.Rule`：有序参考规则链。
- `policycheck.Pred`：架构、调用号、六参数掩码/无符号区间、`ArgsEqual`、`ArgMaskLEArg` 和 `NamedPred`。
- `policycheck.DAG` / `DAGNode` / `Edge`：只能引用更大下标的前向判定 DAG。
- `policycheck.CompilePolicy`：生成严格前向、与参考链一致的规范 DAG。
- `policycheck.Verify` / `VerifyOrError`：符号最小项证明、透明区逐动作等价和可选全枚举。
- `policycheck.VerifyExhaustive`：小位宽六参数全枚举参考。
- `policycheck.BuildProvenanceWithRegistry`、`BuildTraceGraph`：SHA-256 依赖溯源图。
- `policycheck.RecomputeAffectedBranches`、`IssuePartialReuseCertificate`、`VerifyPartialReuseCertificate`：单命名谓词增量重算与 HMAC 部分复用证书。

语义约定：

- 动作优先级：`KILL(60) > TRAP(50) > ERRNO(40) > TRACE(30) > LOG(20) > ALLOW(10)`。
- 多规则命中时保留所有命中规则的 `Audit`/`Notify`；同动作以链中更靠后的命中规则载荷为准。
- 非透明区禁止候选动作弱于参考、禁止同动作载荷串号、禁止审计/通知义务丢失；`Options.Transparent` 或 `Options.Strict` 要求动作、载荷和义务完全一致。
- 符号校验把所有原子谓词展开为两两不相交的完整真值最小项；失败项在 `Report.Mismatches[].Region` 中给出符号赋值、可选具体见证以及 `Reference`/`Candidate` 双方决定。

常用验证命令：

```sh
go test ./...
go vet ./...
```

`selftest` 包含双架构调用号别名、`2^63` 最高位无符号关系、等优先级 ERRNO 载荷覆盖、审计丢失、前向边非法、完整/部分证书篡改和增量依赖分支测试。
