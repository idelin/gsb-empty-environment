# contentmatch

入侵检测引擎的规则用content、offset、depth、distance和within描述多个模式的相对位置。给Go检测引擎实现流式规则匹配包：所有规则共用一个多模式自动机，载荷分段到达时模式可以跨段；相对约束以上一个匹配的位置计算，失败时回溯尝试其他候选；每条流的状态内存有上限。与在完整载荷上逐条规则暴力匹配的结果对照，并枚举分段位置。

## 用法

```go
rules := []contentmatch.Rule{{
    Patterns: []contentmatch.Pattern{
        {Content: []byte("GET"), Offset: 0, Depth: 8},
        {Content: []byte("Host:"), Distance: 0, Within: 64},
    },
}}
eng, _ := contentmatch.NewEngine(rules,
    contentmatch.WithMaxCandidatesPerRule(256))

st := eng.NewStream() // 每条流一个状态，内存有上限
for _, seg := range segments {
    for _, m := range st.Feed(seg) {
        // m.Rule 为规则下标，m.Spans 为各 content 的流内偏移
    }
}
```

约束语义（匹配区间为 `[start, end)`，上一匹配结束于 `prevEnd`）：

- `Offset`：`start >= Offset`；`Depth`：`end <= Offset+Depth`（0 表示不限）
- `Distance`：`start >= prevEnd+Distance`；`Within`：`end <= prevEnd+Distance+Within`（0 表示不限）

实现要点：所有规则共享一个 Aho-Corasick 自动机，载荷只扫描一遍，模式天然可跨分段；每条流只保存自动机状态、字节计数和候选部分匹配，候选数按规则封顶（`WithMaxCandidatesPerRule`），超出淘汰最旧者并计入 `Stream.Dropped`，带 `Within`/`Depth` 窗口的候选过期即剪枝；每个模式出现会尝试扩展所有存活候选，等价于对候选组合的回溯搜索。

测试（`contentmatch_test.go`）以完整载荷上逐条规则暴力回溯匹配为对照：随机规则/载荷下枚举所有单点分段与逐字节分段，并对短载荷枚举全部 2^(n-1) 种分段，断言匹配集合一致。
