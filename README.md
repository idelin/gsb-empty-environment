# Biscuit衰减块事实隔离

Biscuit 令牌允许持有者追加衰减块，若后加的块能给自己添加 authority 事实，持有者就能自行提权。
本目录是可导入的 Go 包 `netsec`（module `example.org/netsec/q0661`），只处理本题约定的
Datalog 子集（事实 `pred(常量…)`、规则 `head :- body`、`check if`、`allow if`、`deny if`，
变量以 `$` 开头）。

## 信任模型

- 块 0 为 authority 块，其余为衰减块。
- authority 块的事实与规则对所有块可见。
- 衰减块自己声明或推出的事实只对本块的 check 可见，不进入 allow/deny 策略所用的事实集合。
- 衰减块里 head 谓词与 authority 同名的规则，其推导事实只存在本块私有作用域，
  绝不写回 authority 作用域，因此无法靠同名规则伪造 `right` 提权。
- 所有块的 check 都必须通过，再按书写顺序评估服务端 allow/deny 策略，第一条匹配者决定，
  无匹配则拒绝。
- 规则在每个作用域内迭代到不动点，最多 100 轮（`MaxRounds`），超限拒绝。

## API 速览

- `ParseToken(blocks ...string) (Token, error)`：解析令牌，第一个源是 authority 块。
- `ParseBlock(index int, src string) (Block, error)`：解析单个块。
- `ParseAuthorizer(src string) (Authorizer, error)`：解析服务端事实与 allow/deny 策略。
- `Evaluate(token Token, auth Authorizer) Result`：执行信任评估，返回决定、命中策略下标、
  出错块下标与哨兵错误（`ErrCheckFailed`、`ErrFixedPointLimit`、`ErrPolicyDenied`、
  `ErrNoPolicyMatched` 等，均可用 `errors.Is` 判别）。

## 示例

```go
token, err := netsec.ParseToken(
    // authority 块
    `
right("alice", "read", "file1")
check if right("alice", "read", "file1")
`,
    // 衰减块：试图给自己追加 write 权限
    `
right("alice", "write", "file1")
check if right("alice", "write", "file1")
`,
)
if err != nil { /* ... */ }

auth, err := netsec.ParseAuthorizer(`allow if right("alice", "write", "file1")`)
if err != nil { /* ... */ }

res := netsec.Evaluate(token, auth)
// res.Allowed() == false：衰减块伪造的 right 不进入策略事实集合，
// res.Reason 命中 netsec.ErrNoPolicyMatched。
```

## 测试

`go test ./...` 覆盖：衰减块伪造 `right`（直接事实与同名规则推导两种）、check 失败、
deny 先于 allow 匹配、不动点超 100 轮拒绝，以及多谓词连接/重复变量/常量过滤的变量绑定。
