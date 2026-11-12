# 审计提交组件（audit-commit）

供 Go 操作宿主调用的审计承诺组件。管理员可以删除审计文件，但任何敏感
操作都不能在缺少“可信审计承诺”的情况下继续成功：副作用只在远端见证确认
通过本地真实签名验证、单调序号被不可回退计数器消费、审计条目落盘之后
才执行。

## 威胁模型与保证

- **可信方**：远端见证（持有 Ed25519 私钥）与不可回退单调计数器（例如
  TPM/TEE NV 索引、HSM 计数器或外部单调服务）。
- **不可信/可回滚部分**：本地审计日志所在的普通磁盘、本地机器快照、
  网络路径，以及拥有删文件权限的管理员。
- 组件保证：
  - 每条确认都用真实 `crypto/ed25519` 签名，本地验证通过后才继续；
  - 签名绑定操作摘要、主体、前承诺（哈希链头）和单调序号，域分离的
    定长/长度前缀编码杜绝字段拼接歧义；
  - 本地旧快照无法重用已消费序号：打开时用计数器的 `Next()` 与日志
    最高序号交叉校验，消费由注入的 `MonotonicCounter` 完成，回退报
    `ErrCounterRolledBack`，跳号报 `ErrCounterGap`；
  - 日志删除（`ErrLogMissing`）、删改（签名/链/摘要校验失败报
    `ErrLogTampered`）、截断（与计数器不同步报 `ErrLogCounterDesync`）
    一律失败关闭，敏感操作无法继续；
  - 计数器不可用、见证不可达、确认丢失时**零副作用、零序号消费、零
    日志追加**。
- **明确不保证网络分区下可用（CP 取舍）**：分区时见证回调失败，组件
  选择一致性与可审计性，直接拒绝操作而不是放行。

## 提交流程（`Committer.Commit`）

1. `counter.Next()` 预留单调序号；失败 → 直接返回，不联系见证。
2. 计算操作摘要，携带序号、主体、前承诺回调见证请求确认。
3. 本地用见证公钥验证 Ed25519 签名及所有绑定字段（`VerifyConfirmation`）。
4. `counter.Consume(seq)` 不可回退地消费序号；失败 → 丢弃该确认，无
   任何副作用，重试会取得新确认且同一序号不会被消费两次。
5. 审计条目经临时文件 + rename 原子写入并 fsync 文件与目录。
6. 仅在以上全部成功后执行宿主注入的 `SideEffect`。

崩溃窗口说明：步骤 5 与 6 之间若进程崩溃，审计条目已落盘但副作用可能
未执行，宿主应把副作用设计为幂等并以审计序号做重放判断；组件本身不会
在没有承诺的情况下执行副作用。

## 关键类型

- `Witness` / `LocalWitness`：见证接口与进程内真实 Ed25519 实现；
  `HTTPWitness` + `WitnessHandler` 提供远端 HTTP 回调形态。
- `MonotonicCounter`：注入式不可回退计数器接口；`MemoryCounter` 是
  参考实现/测试用（不防整机快照，生产必须接入硬件或外部服务）；
  `FailCounter` 用于故障注入测试。
- `Store`：普通磁盘上的 JSON-lines 可回滚存储，全量签名 + SHA-256
  哈希链校验，原子重写。
- `Open` / `Recover`：前者校验既有日志并与计数器交叉核对；后者在
  显式处置（换新计数器）后重建空日志，拒绝复用已前进的计数器。

## 用法示例

```go
pub, priv, _ := ed25519.GenerateKey(nil)
witness := taskpkg.NewLocalWitness(priv)
committer, err := taskpkg.Recover(ctx, taskpkg.Config{
    LogPath: "/var/log/ops/audit.log",
    Witness: witness,
    Counter: hardwareMonotonicCounter, // 实现 taskpkg.MonotonicCounter
})
if err != nil { /* 失败关闭：拒绝启动敏感操作 */ }

err = committer.Commit(ctx,
    taskpkg.Operation{Subject: "admin-7", Summary: "delete-user-42"},
    func(ctx context.Context) error { return doDelete(ctx, 42) },
)
```

## 测试

`go test ./...`（含 `-race`）覆盖：正常提交与重开、哈希链、副作用
时序、跨操作重放、旧快照/回退计数器、计数器 `Next`/`Consume` 不可用、
丢确认安全重试、日志篡改与截断、管理员删文件、伪造签名、HTTP 见证
往返与模拟网络分区、计数器回退/跳号守卫，共 12 个用例。
