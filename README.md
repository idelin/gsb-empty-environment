# TPM Policy Session Transaction Layer

这个 Go 包为密钥代理提供单次、可审计的 TPM 策略会话事务。TPM 命令通过
`Transport` 注入，因此可以接真实 TPM、vTPM 或测试设备。

核心目标不是“生成一条看起来正确的命令”，而是保证 **审批对象就是最终执行
的 TPM 命令**：命令码、规范 Name 序列和精确参数区共同进入 cpHash，策略
承诺安装后命令模板不可变，最终命令执行期间会话保持独占。

## 主要不变量

- 会话绑定 `StartupCount`、session handle 代次、hash 算法、`nonceTPM`、
  `policyDigest` 和 `Principal`。
- `CommandTemplate.CPHash` 使用
  `H(commandCode || Name1...NameN || parameters)`；不会忽略参数尾字节。
- 授权固定执行 `PolicyCpHash`。可同时选择 `PolicyNameHash` 和
  `PolicyOR`，三者都保存已承诺集合和摘要变更链。
- `PolicyCpHash` 成功前深拷贝模板，成功后 `Prepared` 只提供授权值填充入口。
  原调用方继续修改 handles、Names 或 parameters 不会影响最终命令。
- `Authorize`、`Submit`、`Reconcile` 与 `ContextSave`/`ContextLoad`/reset
  共享 Authority 生命周期锁；一个会话在命令响应验证结束前不能被另一个协程
  重新授权。
- 每次最终提交前都会调用 `TPM2_PolicyGetDigest` 并与主机计算的摘要比较，
  防止两侧摘要状态分叉。
- 提交超时或响应无法解析时进入 `StateUnknown`。此时不得授权新命令、Flush、
  ContextLoad 或重放；必须用新的 Quote 和 audit 证据调用 `Reconcile`。
- `ContextSave`、`ContextLoad`、`FlushContext` 和 TPM reset 都会推进 handle
  代次。即使数字 handle 相同，新代次也不会继承旧 `cpHash`/`NameHash`/`OR`
  承诺。

## 基本用法

```go
transport := myTPMTransport{} // 实现 Execute(ctx, []byte) ([]byte, error)
authority, err := netsec.NewAuthority(transport, startupCount)
if err != nil {
    return err
}

session, err := authority.Open(netsec.SessionOptions{
    Handle:         policySession,
    HashAlg:        netsec.AlgSHA256,
    NonceTPM:       nonceTPM,
    PolicyDigest:   initialPolicyDigest,
    Principal:      "nv-agent",
    StartupCount:   startupCount,
    VerifyResponse: verifyTPMResponse,
})
if err != nil {
    return err
}

nvName, err := netsec.NameFromPublic(netsec.AlgSHA256, nvPublicArea)
if err != nil {
    return err
}
template := netsec.CommandTemplate{
    Code:       netsec.CCNVWrite,
    Handles:    []netsec.Handle{authHandle, nvIndex},
    Names:      []netsec.Name{authName, nvName},
    Parameters: nvWriteParameters(data, offset),
}

approved, err := session.Authorize(ctx, template, netsec.AuthorizeOptions{
    NameHash: true,
    OR: &netsec.ORSelection{
        Branches: [][]byte{branchA, branchB},
        Selected: selectedBranch,
    },
})
if err != nil {
    return err
}

// HMAC 必须根据 approved.CPHash()、nonce 和绑定会话的密钥计算。
if err := approved.SetAuth(netsec.AuthValues{
    SessionHandle: session.Binding().SessionHandle,
    NonceCaller:   nonceCaller,
    Attributes:    netsec.AttrContinueSession,
    HMAC:          commandHMAC,
}); err != nil {
    return err
}

response, err := approved.Submit(ctx)
if errors.Is(err, netsec.ErrOutcomeUnknown) {
    // 不得重试。重新 Quote/审计后构造 RecoveryProof 调用 Reconcile。
}
```

NV index 的 Name 通常是 `nameAlg || H(nvPublic)`，不能从数字 handle 猜测。
永久 HR handle 等可直接用 `NameFromHandle`。

同一 TPM/session handle 的所有 `Policy*`、Context 和最终命令都必须经过同一
`Authority`。如果其他代码绕过该实例直接向 TPM 发送策略命令，宿主锁无法阻止
TPM 侧的并发修改；提交前的 `PolicyGetDigest` 能检测已经发生的分叉，但不能
替代单一 TPM 通道这一前提。

## Context 回滚与 reset

```go
blob, err := session.ContextSave(ctx)
// 旧代次立即失效。
restored, err := authority.ContextLoad(ctx, blob, netsec.SessionOptions{})
```

默认只接受本 `Authority` 保存过、且 binding 完全匹配的 context。跨进程恢复需要
独立认证 context 记录，并显式设置 `AllowUnregisteredContext: true`；此时主机端
旧承诺历史不可用，不应把该 context 当作“未承诺”证据。

`TPMReset(newStartupCount)` 与 `ObserveStartup(newStartupCount)` 会使所有旧会话
失效。旧 `Prepared.Submit` 返回 startup/generation 错误，不会落到 reset 后的
新 TPM 状态。

## 超时恢复

`Submit` 对传输错误、超时、响应截断和响应 nonce 异常都采取保守策略：
`StateUnknown`，且不重试。恢复必须提供：

- 当前 `StartupCount`、session handle 和新代次；
- 原 `cpHash`；
- 新的 `Quote` 和 TPM audit 证据；
- `RecoveryOutcome`（已执行或未执行）。

验证函数由宿主注入（`SessionOptions.VerifyRecovery`）。验证成功后会话进入
`StateConsumed`，不会自动重放命令；需要继续操作时应 Flush 并经新会话重新审批。

`VerifyResponse` 同样应验证 TPM 返回授权区的 HMAC；事务层负责 framing、
responseCode、nonceTPM 长度和状态转换，但不持有会话密钥。

## 验证

测试中的假 TPM 会重新计算实际最终命令的 cpHash，并与 `PolicyCpHash` 承诺比较。

```sh
go test -race ./...
```

覆盖场景：

- 两个协程同时交换 NV index；
- `ContextSave`/`ContextLoad` 回滚后同数字 handle 的新代次；
- TPM reset；
- `PolicyOR` 错支；
- parameters 尾字节及 handle 修改；
- 提交前 policyDigest 被修改；
- 超时未知状态和审计恢复；
- 合法一次写入及会话消费。
