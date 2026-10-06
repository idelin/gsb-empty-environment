# dagexec — DAG 任务执行模型

Go 实现的 DAG 任务调度核心，支持三种启动（join）条件，并保证：

- **唯一启动**：每个任务在整个运行中至多被启动一次（`Start`/`Complete` 返回的启动列表不重复）。
- **立即跳过**：启动条件一旦不可能满足，任务立刻置为 `Skipped`，不会永久等待；跳过沿 DAG 级联。
- **原因链**：每个启动/跳过决策都可通过 `Reason`/`Explain` 追溯，父任务的失败（`Failed`）与取消（`Cancelled`）分别记录；原因按父任务 ID 规范化排序，与终态发生顺序无关（汇合性）。

## 启动条件

| 条件 | 启动时机 | 跳过时机（条件不可能满足） |
| --- | --- | --- |
| `AllSuccess` 全成功 | 所有父任务 `Succeeded` | 任一父任务进入非成功终态（失败/取消/被跳过） |
| `AnySuccess` 任一成功 | 任一父任务 `Succeeded` | 所有父任务均已终态且无一成功 |
| `AllTerminal` 全终态 | 所有父任务进入终态（含 `Skipped`） | 不会跳过（父任务必然终态） |

无父任务的是根任务，`Start` 时直接启动。

## API 速览

```go
s := dagexec.New()
s.Add("A", dagexec.AllSuccess)              // 根任务
s.Add("C", dagexec.AllSuccess, "A", "B")    // 全成功
s.Add("D", dagexec.AnySuccess, "A", "B")    // 任一成功
s.Add("E", dagexec.AllTerminal, "C", "D")   // 全终态

launched, _ := s.Start()                    // 启动根任务
launched,  _ = s.Complete("A", dagexec.Failed) // 记录终态，返回新启动的任务
st    := s.State("C")                       // 当前状态
why   := s.Reason("C")                      // 决策 + 原因（区分 failed/cancelled）
chain := s.Explain("E")                     // 递归打印原因链
```

执行器循环：把 `Start`/`Complete` 返回的 ID 投入运行，任务结束时用其结果调用
`Complete`，直到没有运行中的任务。`Complete` 对非运行中任务或非法结果返回错误，
从机制上保证唯一启动、唯一完成。

## 验证

`dag_test.go` 中的 `TestExhaustiveTerminalOrderings` 用深度优先搜索枚举小 DAG
（A、B 根任务；C=全成功；D=任一成功；E=全终态）的**全部终态顺序 × 全部结果组合**
（294 种终态序列、75 种结果分配），逐一核对：

1. 无永久等待：运行结束时所有任务均为终态；
2. 唯一启动：被跳过的任务从未启动，启动的任务恰好完成一次；
3. 决策一致：用最终父任务状态重新求值，启动 ⇔ 条件满足，跳过 ⇔ 条件不可能；
4. 原因链正确：记录的原因与规范化原因集一致，且与父任务实际终态吻合；
5. 汇合性：相同结果分配下，任意终态顺序产生完全相同的最终状态与原因链。

运行：

```sh
go test ./... -v
```
