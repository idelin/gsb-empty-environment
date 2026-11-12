# 早期数据副作用门控

接入服务允许握手完成前提交只读请求，但可能误把带副作用请求一起执行。写供Go请求宿主调用的早期数据门控包，使用调用方提供的不可变操作分类与身份需求，未完成握手仅允许可重放只读操作；握手拒绝后不得重放已执行操作为普通请求。用事件模型测试分批解析、分类更新及确认与取消竞争。

## earlygate 包

实现位于 `earlygate/`，核心是一个纯事件归约内核（`kernel.go`）与带互斥
锁的宿主门面 `Gate`（`gate.go`）：

- `Classifier[O]` 由调用方实现，给出每个操作的 `Classification`
  （`ReadOnly`、`Replayable`、`IdentityRequirement`）；分类在首次提交时
  固化，`UpdateClassifier` 只影响新请求。
- 握手未完成时，仅 `ReadOnly && Replayable && IdentityNone` 的操作得到
  `ExecuteEarly`，其余得到 `HoldEarly` 并记录挂起原因。
- `Confirm` 把挂起请求转为 `DispatchNormal`；早期请求保持自身执行路径，
  绝不再次派发。
- `Reject` 让挂起请求得到 `RejectRequest`，已早期执行的请求转为
  `StatusEarlyUnconfirmed`；`EarlyRequests()` 可用于断言早期/普通派发集合
  不相交。
- 事件类型定义在 `events.go`（分批解析、分类器替换、握手确认/拒绝、早期
  执行回执、单请求取消），决策通过返回值与可选的 `WithDecisionHandler`
  回调同时交付。

测试（`*_test.go`）以事件模型覆盖分批解析、分类更新、确认与取消竞争
（`-race` 下重复 200 轮）、确认/拒绝竞争以及拒绝后不重放不变量：

```sh
go test -race -count=1 ./...
```
