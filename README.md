# errser — 安全的错误图序列化

仅使用 Go 标准库，把错误链与聚合错误序列化为有界 JSON 图文档，并安全解码。

## 设计声明（支持的序列化范围，除此之外一律不支持）

- 节点 = 稳定类型码（`code`，与显示文本分离）+ 显示消息（`message`）+ 上下文字典。
- 边 = 单条 `cause` 链 + 聚合 `children` 列表；共享子错误按身份只序列化一次，用 ID 引用。
- 循环可表示、可恢复：遍历均有访问标记，循环链不会无限递归。
- 上下文字段按白名单输出；非白名单字段只计入每节点 `redacted` 计数，键与值都不输出。
- 上下文值只保留有界标量（字符串/布尔/数值），其他类型输出 `[unsupported]`。
- 解码是纯数据操作：不执行宿主代码、不反射实例化任意类型；所有声明长度先验证再分配。
- 未注册的类型码解码后一律归为通用码 `err.unknown`。

## 非泄漏规则

公开输出只包含：图结构、稳定类型码、显示消息、白名单键值、脱敏计数与截断标记。
因此两份仅敏感值不同的输入，其公开结构与计数字节级一致（测试 `TestWhitelistRedactionAndNonLeakage` 验证）。

## 预算与截断

- 硬预算（返回 `ErrBudgetExceeded`）：节点数 `MaxNodes`、输入字节数 `MaxBytes`、解码端声明的节点/子节点/上下文计数。
- 软限制（设置截断标记 `truncated`）：深度 `MaxDepth`、消息长度、上下文键数、值长度。

## 运行

```sh
go test ./...
go run ./cmd/demo
```

## 运行样例输出

```
=== serialized document ===
{"version":1,"root":0,"nodes":[{"id":0,"code":"err.aggregate","message":"request failed","cause":-1,"children":[1,3]},{"id":1,"code":"api.fetch","message":"fetch profile failed","cause":2},{"id":2,"code":"db.timeout","message":"connection timed out","context":{"op":"dial","retryable":"true"},"redacted":2,"cause":1},{"id":3,"code":"api.fetch","message":"fetch settings failed","cause":2}]}

=== decoded graph (cycle-safe render) ===
err.aggregate: request failed [api.fetch: fetch profile failed <- db.timeout: connection timed out <- api.fetch (shared)] [api.fetch: fetch settings failed <- db.timeout (shared)]

=== identity checks ===
shared cause preserved: true
cycle restored: true

=== non-leakage ===
public output identical for different secrets: true
```

## 测试结果

```
=== RUN   TestRoundTripChainAndAggregate      --- PASS   (链+聚合+共享原因身份保留)
=== RUN   TestCycleTerminatesAndRestores      --- PASS   (循环序列化终止且按身份恢复)
=== RUN   TestWhitelistRedactionAndNonLeakage --- PASS   (白名单/脱敏计数/非泄漏字节一致)
=== RUN   TestTruncationMarkers               --- PASS   (深度/消息/上下文截断标记)
=== RUN   TestBudgetFailures                  --- PASS   (节点预算/输入字节/声明计数预算失败)
=== RUN   TestUnknownCodeDecodesToGeneric     --- PASS   (未知类型码归一，不实例化宿主类)
=== RUN   TestExhaustiveSmallGraphs           --- PASS   (512 个有限图穷举：自环/互环/共享)
=== RUN   TestMutationRobustness              --- PASS   (652 个字节变异+截断前缀，无 panic)
=== RUN   TestExternalErrorWrappedAsGeneric   --- PASS   (外部错误归为通用外部节点)
PASS
ok  	errser/errser
```
