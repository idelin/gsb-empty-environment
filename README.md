# 符号协议认证对应性

协议教学平台需要在两条会话间寻找认证错配。写供Go验证宿主调用的符号执行包，角色脚本最多十二步，操作为发消息、按模式接收、生成新原子以及begin和end事件；消息由原子、二元组和对称加密构成。攻击者可拆已知二元组、用已知钥解密及组合深度不超过三的消息，不能猜新原子。寻找end主体与随机数没有对应begin的最短交错轨迹；预算耗尽返回未知。用可重放推导证书验证每条攻击者消息均可构造。

## 用法

```go
k := netsec.AtomOf("k")
n := netsec.VarOf("n")
p := &netsec.Protocol{Roles: []netsec.Role{
    {Name: "init", Steps: []netsec.Step{
        netsec.FreshAtom("n"),
        netsec.SendMsg(netsec.EncOf(k, n)),
        netsec.RecvMsg(netsec.EncOf(k, n)),
        netsec.EndEv(netsec.AtomOf("A"), n),
    }},
}}
spec := &netsec.SessionSpec{Role: "init"}
res, _ := netsec.FindMismatch(p, spec, spec, nil, 100000)
// res.Status == netsec.Attack，res.Trace 为最短交错轨迹
err := res.Verify(p, spec, spec, nil) // 重放推导证书
```

- `FindMismatch(p, sA, sB, initial, budget)`：对两条会话的交错做 BFS；返回 `Attack`（含最短轨迹）、`Safe` 或预算耗尽时的 `Unknown`；`budget < 0` 表示不限。
- 攻击者知识对二元组投影与已知钥解密封闭；合成消息的组合深度不超过 `MaxComposeDepth`（3），新原子不可猜测。
- 攻击轨迹即证书：每条 `recv` 携带 `Derivation` 证明，`Result.Verify` 重放整条轨迹并校验每片叶子均可由当时攻击者知识推出。
