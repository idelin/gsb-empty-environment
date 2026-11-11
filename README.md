# 多域拒收解释最小证据

邮件策略服务希望解释一次拒收究竟依赖哪些认证结果，不能把缺失当失败。写供Go服务调用的三值决策包，输入pass、fail、unknown原子事实及AND、OR、NOT规则，采用强Kleene逻辑，只有最终false拒收、unknown待定；求维持当前结论所需最少事实，未选事实视为unknown，同数量按ID排序。最多二十事实，用全部子集验证最小解释及相互共享子式。

## 用法

```go
c := netsec.NewCompiler()
rule := c.And(
    c.Or(c.Fact("spf"), c.Fact("dkim")),
    c.Not(c.Fact("dmarc")),
)

// 缺失的事实按 unknown 处理，绝不等于 fail。
facts := map[string]netsec.Value{
    "spf":   netsec.Fail,
    "dkim":  netsec.Fail,
    "dmarc": netsec.Pass,
}

rule.Decide(facts) // netsec.Reject
ex := rule.Explain(facts)
// ex.Decision == Reject，且 ex.Facts 是维持拒收所需的最少事实；
// 未选事实一律按 unknown 处理，同数量按事实 ID 字典序决胜。
```

## 设计要点

- `Value` 三值：`Pass` / `Fail` / `Unknown`；`And`、`Or`、`Not` 为强 Kleene 运算。
- `Compiler` 把规则编译为 DAG，结构相同的子式归并为同一节点并可被多父共享；
  `Rule.Eval` 按节点记忆化，共享子式只算一次。
- `Decide`：`Fail → Reject`、`Unknown → Pending`、`Pass → Allow`。
- `Explain` 在至多 20 个事实上按规模升序、同规模按 ID 字典序枚举子集，
  第一个仍推出当前决策的子集即最少证据。
- 测试用独立朴素求值器与全子集暴力枚举交叉校验（`go test ./...`），
  并覆盖共享子式、20 事实上限及最坏情况 benchmark。
