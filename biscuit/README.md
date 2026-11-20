# biscuit — Biscuit 块信任评估包

`biscuit` 是一个可直接 import 的 Go 包，实现题目约定的 Datalog 子集，
用于评估 Biscuit 令牌的 authority 块与衰减块，并执行服务端 allow / deny 策略。

导入路径：`prescreen.local/ddd-reczz28zozlo8l2e/biscuit`

## 语法子集

```
# 事实（参数只能是常量）
right("alice", "read")

# 规则（变量以 $ 开头，head 变量必须出现在 body 中）
grandparent($g, $c) :- parent($g, $p), parent($p, $c)

# 块约束，多个查询用 || 连接（或关系）
check if right($u, "read") || right($u, "admin")

# 服务端策略（只出现在 ParsePolicies 中）
allow if right($u, "read")
deny if revoked($u)
allow if true
```

常量支持标识符（`admin`）、字符串（`"alice"`）、64 位整数（`-7`）、
布尔（`true` / `false`）；`#` 开始行注释，语句可用换行或分号分隔。

## 信任与作用域

- 块 0 是 authority 块，其事实与规则推出的事实构成 authority 作用域，对所有块只读可见。
- 其余块是衰减块，在各自的私有作用域内求值；衰减块推出的事实只对本块 `check` 可见，
  不进入 allow / deny 使用的事实集合。
- 衰减块中 head 谓词与 authority 同名的规则，新事实只写入私有作用域，
  无法伪造 authority 事实提权。
- 所有块的 `check` 必须全部通过；之后按顺序评估服务端策略，第一条命中者决定结论，
  无任何命中则默认拒绝。
- 规则前向链迭代到不动点，单个作用域最多 `biscuit.MaxFixpointRounds`（100）轮，
  超限即拒绝（`Reason == ReasonFixpoint`）。

## 快速上手

```go
authority, _ := biscuit.ParseBlock(`
user("alice")
right("alice", "write")
check if user("alice")
`)
attenuated, _ := biscuit.ParseBlock(`
right("alice", "read")
check if right("alice", "read")
`)
policies, _ := biscuit.ParsePolicies(`
allow if right($user, "read")
deny if true
`)

auth, _ := biscuit.New([]*biscuit.Block{authority, attenuated}, policies)
result := auth.Authorize()
// 衰减块伪造的 right 不进入策略作用域：deny 兜底命中，结论为 deny。
fmt.Println(result.Decision, result.Reason) // deny deny_policy
```

## 测试

```sh
go test ./...
```

测试覆盖：衰减块伪造 `right` 事实、同名 head 规则不写回 authority、check 失败、
deny 先匹配、100/101 轮不动点边界与超限拒绝、变量绑定与多谓词连接、
authority 规则对衰减块可见，以及解析与规则安全性错误。
