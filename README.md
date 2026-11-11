# 解析器位置保持变异

二进制安全测试需要变异载荷但维持跨区段引用有效。给Go模糊测试框架写可导入的结构变异包，区段由字节串、对齐量、长度字段、绝对或相对偏移字段构成；插入删除后统一重定位，长度溢出拒绝整个候选，引用环允许但布局依赖环拒绝。用独立读回器核对正负相对偏移、零长区段和重复引用，失败不得修改原样本。

## structmut 子包

导入路径：`example.org/netsec/uid1245/structmut`

- `Schema`/`Section`/`Field` 描述字节串区段、对齐量（2 的幂，1–64）、
  `KindLength`/`KindAbsOffset`/`KindRelOffset` 字段（宽度 1/2/4/8，
  相对偏移为有符号，支持负值）。
- `Schema.Apply(Insert(...), Delete(...), SetBytes(...))` 在深拷贝上变异，
  字段占用区间受保护；插入/删除统一重定位字段与全部跨区段引用，
  并按 `Section.After` 做确定性拓扑布局和对齐填充。
- 长度/偏移超宽返回 `ErrLengthOverflow`/`ErrOffsetOverflow`，
  布局依赖环返回 `ErrLayoutCycle`，整个候选被拒绝；
  字段引用成环（引用环）合法。
- `Read(blob, schema)` 是独立读回器：自带拓扑排序、放置与
  `encoding/binary` 解码，核对长度、绝对偏移、正负相对偏移、
  零长区段、重复引用和填充字节，失败以 `ErrVerification` 包装返回。
- 任何失败都不修改入参 `Schema`；`Schema.Random(rng, maxOps)` 供
  `fuzz.F` 直接调用，`FuzzApply` 为示例模糊目标。

运行：

```
go test ./...
go test ./structmut/ -fuzz=FuzzApply -fuzztime=30s
```
