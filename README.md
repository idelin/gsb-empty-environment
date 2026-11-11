# 协议重放知识时点

攻击者在会话结束后才拿到密钥，不能把它提前用于解释此前消息。写供Go符号验证宿主调用的知识闭包包，事实带首次可得事件序号，二元组拆分与对称解密取前提最晚时点；计算目标秘密最早可知时刻及推导DAG。新事实迟到增量更新，用全量重算核对共享推导和密钥后来泄露。

## 模型

包 `netsec` 在 Dolev-Yao 风格的消息代数上维护攻击者知识闭包：

- 项：名字 `a*`、新鲜值 `n*`、对称密钥 `k*`、二元组 `pair(t1,t2)`、对称加密 `enc(t,k)`。
- 事实 `Fact{Term, AtEvent}`：攻击者在事件 `AtEvent`（从 1 开始）首次观察到某个项；同一事实迟到的更早事件会收紧首次可得时间。
- 每条推导边的触发时刻等于所有前提首次可得时刻的最大值，因此会话结束后才泄露的密钥不可能被提前用于解释旧密文。

闭包规则：

| 规则 | 前提 | 结论 | 触发时点 |
| --- | --- | --- | --- |
| `fact` | 观察事实 | 该项 | 事实事件 |
| `fst` / `snd` | `pair(x,y)` | `x` / `y` | 元组可得事件 |
| `dec` | `enc(x,k)`, `k` | `x` | `max(密文事件, 密钥事件)` |
| `pair` | `x`, `y` | `pair(x,y)` | `max(x事件, y事件)` |
| `enc` | `x`, `k` | `enc(x,k)` | `max(x事件, k事件)` |

会形成自环的构造边（例如用包含 `x` 的复合项反推 `x` 本身）被直接拒绝，保证目标推导始终是 DAG。

## 宿主调用

```go
engine := netsec.NewEngine()
secret, _ := netsec.ParseTerm("n_secret")
target, _ := engine.SetTarget(secret)

_ = engine.AddFactString("enc(n_secret,k_session)", 1) // 会话流量
_ = engine.AddFactString("k_session", 4)                // 会话结束后泄露

at, known := engine.Earliest(target) // known=true, at=4
edge, _ := engine.Derivation(target) // edge.Rule == netsec.RuleDec
report := engine.Report()            // 含事实、已知项、目标时点与推导 DAG
raw, _ := engine.JSON()              // 供宿主落盘或跨进程传递
```

`AddFact` 采用半朴素工作列表做增量定点传播；每次变更后默认执行 `Check`，它调用独立的 `Recompute`：

1. 从全部事实重新播种，用与增量引擎不同的全量扫描定点算法重算最早事件；
2. 逐项核对最早可知时刻、所选共享推导边（同时间按规则序确定性破并）；
3. 校验目标可达 DAG 的时点单调性，覆盖密钥后泄露与迟到事实场景。

可用 `WithAutoVerify(false)` 关闭自动核对、在需要时显式调用 `Check`。

## 验证

- `go test ./...`：解析、元组拆分、对称解密、嵌套解密、迟到事实、构造环拒绝、200 组随机事实序列的增量/全量一致性。
- `go run ./cmd/q0467-demo`：输出密钥后泄露场景的完整 JSON 报告与推导 DAG。
