# 授权检查支配关系

代码审查服务需要发现敏感操作是否总在匹配对象的授权检查之后执行。写供Go分析宿主调用的图分析包，输入控制流图，节点包含校验主体与对象、修改对象版本、转移授权凭据、敏感调用和异常边；凭据只能证明原主体、原对象及原版本，合流取所有路径共同保证。循环求有限不动点，返回缺少保证的调用及最短路径。用小图具体执行验证校验另一对象、异常绕过与修改后继续使用旧凭据。

## 包入口

`netsec.Analyze(Graph) (*Result, error)`，无第三方依赖，宿主把过程内控制流
翻译成 `Graph` 即可调用：

```go
result, err := netsec.Analyze(netsec.Graph{
    Entry: "entry",
    Nodes: []netsec.Node{
        {ID: "entry", Kind: netsec.KindPlain},
        {ID: "check", Kind: netsec.KindAuthCheck, Subject: "alice", Object: "doc"},
        {ID: "modify", Kind: netsec.KindModifyVersion, Object: "doc"},
        {ID: "call", Kind: netsec.KindSensitiveCall, Subject: "alice", Object: "doc"},
    },
    Edges: []netsec.Edge{
        {From: "entry", To: "check"},
        {From: "check", To: "modify"},
        {From: "modify", To: "call"},
    },
})
```

## 语义

- 抽象状态记录每个对象的共同当前版本，以及所有到达路径共同持有的
  `(主体, 对象, 版本)` 凭据（must 集合）。
- `KindAuthCheck` 按对象当前版本发凭据；`KindModifyVersion` 提升版本，
  旧凭据保留但与新版本不匹配；`KindTransferCredential` 原样传递凭据，
  不扩大主体；`KindSensitiveCall` 要求入口存在完全匹配的凭据。
- 合流对版本与凭据取交集，版本不一致退化为 `UnknownVersion`；异常边
  `EdgeException` 与普通边同等参与合流和路径枚举。
- 循环采用单调混沌迭代，格有限必收敛，轮数见 `Result.Iterations`。
- 每个报警 `Finding` 含缺失原因与一条边数最少的违反路径 `Path`，
  路径上的异常边在 `PathStep.Via` 标注；`Result.StateAt` 可查任意节点
  的不动点状态。

## 验证

`analyze_test.go` 用小图具体执行了要求的三个场景及补充场景：

- `TestWrongObject`：校验另一对象不能覆盖目标对象的敏感调用。
- `TestExceptionBypass`：异常边绕过检查，最短路径走异常边。
- `TestStaleCredentialAfterModify`：版本修改后旧凭据不匹配，重新授权后通过。
- `TestTransferDoesNotExtendAuthority`：凭据转移不把授权扩大到新主体。
- `TestLoopFixedPoint`：循环多轮迭代收敛到有限不动点。
- `TestMergeIntersection`、`TestUnreachableAndValidation`：合流交集与结构校验。

运行：

```sh
go test ./...
```
