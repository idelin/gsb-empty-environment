# 认证事件注入对应

协议中的同一开始事件可能被重复结束事件消费，形成重复认证。写供Go验证宿主调用的事件匹配包，begin与end携带主体、会话随机数和操作摘要，要求每个end匹配一个更早且未用过的begin；允许并发会话和部分未结束。找是否存在合法注入匹配，失败返回Hall型缺口证据，用所有匹配枚举核对。

## 模型

把 begin 与 end 建成二分图：当且仅当两端的主体、会话随机数、操作
摘要完全一致，且 begin 在事件流中早于 end 时连一条边。合法匹配要求
每个 end 占用一个不同的 begin（一对一），从而杜绝同一 begin 被重复
消费；并发会话由会话随机数天然隔离，未被消费的 begin 视为部分未结束。

## 入口

- `Analyze(events)`：增广路判定是否存在覆盖全部 end 的完整匹配；
  失败时返回 `HallWitness`（end 子集 `Ends` 及其全部相容 begin
  `NeighborBegins`，满足 `|N(S)| < |S|`），成功时给出一个匹配和
  `UnmatchedBegins`。
- `EnumerateMatchings(events, limit)`：回溯枚举所有完整合法匹配，
  `limit <= 0` 时用 `DefaultEnumerationLimit`，超限置 `Truncated`。
- `CrossCheck(events, limit)`：用最大匹配、Hall 证据独立校验
  （`ValidWitness`/`ValidMatching`/`IsComplete`）与全量枚举三条路径
  互相核对，返回 `Consistent` 总判定。

事件先后顺序由切片下标决定，宿主按抓包/日志顺序传入即可。

```go
report, err := netsec.Analyze(events)
if err != nil { /* 非法事件类型 */ }
if !report.Feasible {
    // report.Witness 即 Hall 型缺口证据
}
check, _ := netsec.CrossCheck(events, 0)
_ = check.Consistent
```

## 验证

`go test ./...` 覆盖重复消费、时序约束、字段不符、并发会话、部分
未结束、枚举数量与截断，并以 300 组随机流和独立朴素回溯实现核对
可行性与匹配总数。
