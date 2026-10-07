# replay — 重放执行器

将状态计算与外部效果分离的事件重放执行器。

## 设计

- **状态计算是纯函数**：`Reducer(state, event) -> (state, []EffectSpec)` 只折叠状态并声明效果，不执行。
- **效果只是描述**：`Effect` 携带 `ID = 事件ID#序号`（`EffectID`），由执行器统一分配，重放时可确定性再生。
- **已确认效果不再执行**：`Journal` 持久记录事件流与已确认效果 ID；`Recover` 重放时只重新生成效果描述，已确认的跳过，仅补发未确认效果。
- **回执丢失可重发同 ID**：`Sender.Send` 返回错误视为回执丢失，执行器以相同效果 ID 重试直至收到回执后写入确认。
- **接收方幂等**：`Receiver` 以效果 ID 为键的幂等表去重，重复投递只确认不重复应用；`DropReceipt` 钩子可模拟"已应用但回执丢失"。
- **重复事件去重**：`Journal.AppendEvent` 按事件 ID 去重，重复事件不改变状态也不产生新效果。

## 文件

- `event.go` / `effect.go` — 事件与效果描述，效果 ID 派生规则
- `journal.go` — 事件日志 + 已确认效果集合（跨重启共享）
- `executor.go` — `Handle`（去重、折叠、派发）与 `Recover`（重放、补发未确认效果）
- `receiver.go` — 带幂等表的虚拟接收方，可模拟回执丢失
- `account.go` — 示例归约器（账户余额 + 台账/通知效果）
- `executor_test.go` — 恢复、重复事件、回执丢失重发等场景测试

## 测试

```sh
GO111MODULE=on go test ./...
```
