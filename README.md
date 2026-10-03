# 长事务补偿与状态机持久化引擎（Saga Engine）

纯 Go 标准库实现的长事务引擎：把跨越数小时的异步业务事务切分为
「状态迁移 + 外部调用」的步骤序列，支持超时/外部拒绝触发的逆向
补偿（Saga），并通过写前日志（journal）持久化状态机，保证断电
续跑不重不漏。

## 设计

- **步骤模型**（`saga.Definition` / `saga.Step`）：每个步骤含
  `Action`（外部调用）与可选 `Compensate`（逆向补偿）。`Action`
  返回 `ErrAwait` 表示已提交外部请求、进入异步等待，由
  `Engine.Deliver` 投递回调或超时监控唤醒。
- **持久化状态机**（`saga/journal.go`）：每个事务一个 JSONL 日志
  文件，每次状态迁移先 append + fsync 再生效。内存状态是日志事件
  的纯函数（`Tx.apply`），恢复回放与实时执行走同一条路径，二者
  不会分叉。
- **断电续跑不重不漏**：
  - 已完成步骤：日志中已有 `StepCompleted`，恢复后直接跳过（不重）。
  - 崩溃在「外部调用已生效、完成事件未落盘」之间：恢复后以**同一
    幂等键**（`txID:stepName`）重试，外部系统按键去重（不漏）。
  - 等待回调期间崩溃：恢复时从持久化的 `deadline` 重新武装超时
    监控，截止时间已过则立即触发超时补偿。
- **逆向补偿**：超时或步骤失败后，按完成顺序的逆序对已完成步骤
  逐个执行 `Compensate`，补偿进度同样落盘，可断点续偿。
- **审计轨迹**：全部事件（含时间戳、幂等键、输出、错误、截止
  时间）持久化，`Engine.Audit` 可随时读出复核。

## 运行样例

```sh
go run ./cmd/demo            # 付款超时默认 2s（代表数小时）
```

输出（银行永不回调 → 超时 → 撤销采购单 → 通知审批人）：

```
== 发起长采购事务（审批 -> 采购 -> 付款） ==
  [审批系统] 收到审批请求 ...，审批人处理中...
  [采购系统] 创建采购单 PO-0001 ...
  [银行] 收到付款请求 ...（银行故障，永不回调）
  [采购系统] 撤销采购单 PO-0001（补偿动作 ...）
  [审批系统] 已通知审批人：事务 ... 已取消（补偿动作 ...）
== 事务终态: COMPENSATED ==
== 可复核状态审计轨迹（持久化日志） ==
  #01 TxStarted
  #02 StepStarted          step=approve
  #03 AwaitingCallback     step=approve  deadline=...
  #04 CallbackReceived     step=approve  output=APPROVED
  #05 StepStarted          step=purchase
  #06 StepCompleted        step=purchase output=PO-0001
  #07 StepStarted          step=pay
  #08 AwaitingCallback     step=pay      deadline=...
  #09 StepTimedOut         step=pay      err=saga: step timed out ...
  #10 CompensateStarted    step=purchase
  #11 CompensateCompleted  step=purchase
  #12 CompensateStarted    step=approve
  #13 CompensateCompleted  step=approve
  #14 TxCompensated
```

## 测试

```sh
go test ./saga/ -v
```

结果：

```
--- PASS: TestHappyPath                          # 全链路成功，审计序列精确匹配
--- PASS: TestTimeoutTriggersReverseCompensation # 银行超时→逆向补偿（先撤单后通知）
--- PASS: TestCrashRecoveryWhileAwaiting         # 等待银行时断电，重启后超时监控再武装，步骤不重跑
--- PASS: TestCrashMidStepIsIdempotent           # 外部调用生效后落盘前断电，同幂等键重试只生效一次
--- PASS: TestAuditTrailSurvivesRestart          # 审计轨迹跨重启完整、序号连续
PASS
ok  	sagademo/saga
```

（亦通过 `go test -race`。）

## 已知取舍

- 补偿动作自身失败时事务进入 `FAILED` 终态，需人工介入（未做
  无限重试，避免掩盖系统性故障）。
- 外部系统必须按幂等键去重，这是「不重」语义在崩溃窗口内的
  必要协作条件。
