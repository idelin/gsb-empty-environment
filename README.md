# 受限聚合去标：不同主体支持集证明传播

`netsec` 包供 Go 查询宿主调用，在关系算子执行期间为每个值携带
“不同主体支持集”证明（`Proof`），并按不同主体数而非行数决定聚合值
能否去掉敏感标签。

## 核心规则

- **复制不增加人数**：`Value.Copy` 与 `CopyRows` 复用同一支持集，
  投影放大、重复行不会凑出更多主体。
- **连接贡献去重**：`Join` 输出值的证明是源列与两侧连接键主体的并集，
  同一主体跨左右两侧或在一对多扇出的多行命中里只计一次。
- **聚合按不同主体去标**：`GroupAggregate`/`Aggregate` 对输入值证明取
  并集；同一主体多行只贡献一次，达到 `Policy.K` 个不同主体才可去标。
- **过滤后重新评估**：`Filter` 只决定行去留、不改写保留值证明；
  不存在“聚合时去标后永久放行”的粘性标志，`Policy.CanRelease`
  始终针对存活值自带的证明逐值判定。

## 宿主调用示例

```go
policy, _ := netsec.NewPolicy(3)

raw := netsec.NewTable([]string{"grp", "amount"},
    netsec.Row{"grp": netsec.NewValue("A", "alice"),
               "amount": netsec.NewValue(10, "alice", "bob")},
    netsec.Row{"grp": netsec.NewValue("A", "alice"),
               "amount": netsec.NewValue(5, "carol")},
)

grouped := netsec.GroupAggregate(raw, []string{"grp"}, "amount", "total", netsec.SumInts)
released, sensitive := netsec.Materialize(grouped, policy)
_ = released    // 不同主体数 >= K 的行
_ = sensitive   // 仍带敏感标签、不得发给未授权方的行
```

## 反例测试

`go test ./...` 覆盖四类必须防住的场景：

1. 复制 100 行仍只有 2 个主体，K=3 下去不了标。
2. 共享来源 + 一对多连接扇出，连接贡献按主体去重，聚合后才计真实人数。
3. 同一主体多行参与聚合，10 行只有 2 个主体时仍敏感。
4. 聚合后再过滤，收紧阈值后按存活值证明重新判定，不沿用过滤前结论。
