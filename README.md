# 链接攻击候选集保持

匿名数据发布前要判断外部已知属性能否唯一定位个体。实现供Go隐私宿主调用的候选集包，输入匿名记录、缺失或泛化属性及背景知识区间，计算每条外部记录的全部兼容对象；多条观察若声明同一主体则求联合约束，不分别取并集。用小数据全集核对空集、重复匿名行及组合后唯一识别。

## 用法

```go
anon := []netsec.Record{
    {"age": netsec.Range(20, 30), "sex": netsec.Categories("M")},
    {"age": netsec.Range(25, 35), "zip": netsec.Missing()},
}
obs := []netsec.Observation{
    {Subject: "alice", Known: netsec.Record{"age": netsec.Range(24, 26)}},
    {Subject: "alice", Known: netsec.Record{"sex": netsec.Categories("M")}},
}
cands := netsec.SubjectCandidates(anon, obs) // alice 的两条观察取交集
uniq := netsec.UniquelyIdentified(anon, obs) // 候选集大小为 1 的主体
```

- `Domain` 表示属性的取值可能集：`Exact`/`Range`（数值区间）、`Categories`（类别集合）、`Missing`（缺失，匹配一切）。
- `CandidateSet` 计算单条外部观察兼容的全部匿名记录下标。
- `JointCandidates` 对同一主体的多条观察求交集（联合约束），而非并集。
- 运行测试：`go test ./...`
