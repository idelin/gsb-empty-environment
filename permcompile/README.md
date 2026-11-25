# permcompile

可嵌入 Go 导入宿主的许可编译与执行库。所有输入均为内存中的计划、字节区间与
回调；库不做任何 I/O，也不自带加密实现，选择事件的签名校验由宿主通过
`Authenticator` 回调注入。

## 模型

- 计划节点：`KindParallel`（并行）、`KindSequence`（顺序）、`KindChoice`
  （二选一）、`KindFallback`（条件回退），叶子是一次性 `Grant`。
- 叶子许可携带不可变 `ObjectID`、`Generation`、授权 `Scope` 与 `MaxReads`。
- 配额统一用 64 位 `IntervalSet` 表示：字节是“仍可读区间”，读取次数是
  `[0, MaxReads)` 计数区间。完整读取保留字节范围（可凭剩余次数重读），
  只有部分读取会精确切分仍可读区间。
- 每个 `Choice` 必须由绑定本 `SessionID` 与编译期 `PlanDigest` 的已认证
  `Event` 决定；`Nonce` 在会话内防重放。

## 可能世界语义

运行时始终保留全部尚可成立的执行解释（world）：

- `Authorize` 只有在**每个**世界都存在与未决门相容、且能完整覆盖请求区间的
  叶子子集时才返回 `VerdictAllow`；若某些世界只是等待选择/回退事件，返回
  `VerdictWait` 并列出 `WaitEvent`；否则 `VerdictDeny`。
- `Commit` 在每个世界上枚举全部合法消费方式，克隆出后继世界并精确扣减所有
  参与叶子的字节与次数配额，再按规范状态去重，避免“任取一个叶”导致的下次
  误拒或配额超发。
- 未消歧门在首次提交时形成暂定选择（`tentative`），真实事件到达后裁剪矛盾
  世界；取消的子树、过期会话、耗尽配额分支永不重新开读。

## 典型用法

```go
cp, err := permcompile.Compile(plan)
rt := permcompile.NewRuntime(cp, session, authenticator)

switch d := rt.Authorize(req); d.Verdict {
case permcompile.VerdictAllow:
    err = rt.Commit(req) // 或 rt.Serve(req, dataCallback)
case permcompile.VerdictWait:
    // 等待 d.Waiting 中的已认证事件，再重新 Authorize
case permcompile.VerdictDeny:
    // 拒绝转发
}
```

`Serve` 在允许后调用数据回调，回调成功才提交消费；回调错误时配额不变。
`Trace`/`Snapshot` 暴露全轨迹与各世界剩余配额，供小计划解释器核对。
