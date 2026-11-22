# redaction — 审计日志删节证明包

供 Go 审计宿主调用的删节（cut-down）证明：导出时可以隐藏任意记录的内容，
同时证明记录的顺序、隐藏位置没有被删除，且导出物来自同一条日志。

## 设计概览

所有哈希使用 SHA-256，并以单字节域前缀做域分离：

| 前缀 | 用途 |
| --- | --- |
| `0x01` | 条目承诺 `Commitment` |
| `0x02` | Merkle 叶子（承诺再包一层，防二原像） |
| `0x03` | Merkle 内部节点 |
| `0x04` | 最终证明根 |

- 条目承诺（绑定日志 ID、序号、载荷）：
  `C_i = H(0x01 ‖ len(logID) ‖ logID ‖ i ‖ len(payload) ‖ payload)`
- 叶子：`L_i = H(0x02 ‖ C_i)`；层内节点为奇数时复制最后一个节点，构造成
  平衡 Merkle 树，深度为 `ceil(log2(n))`。
- 证明根（再绑定日志 ID 与总条数）：
  `Root = H(0x04 ‖ len(logID) ‖ logID ‖ total ‖ MerkleRoot)`

导出物 `Bundle`：

- 隐去项（`Hidden=true`）：只给 `Index` 与 `Commitment`，不允许携带载荷或路径。
- 公开项：给 `Index`、`Commitment`、原文 `Payload` 和 Merkle 成员路径
  `MerklePath`（左右位置由序号比特推出，无需方向标记）。
- 头部：`LogID`、`Total`、`Root`。`Bundle` 可直接用 `encoding/json` 序列化
  （字节切片以 base64 传输）。

## 宿主调用方式

```go
prover, err := redaction.NewProver("audit-2026-11", payloads)
if err != nil { /* 空日志 */ }

// 公开第 1、3 条，其余隐去；传 nil 表示全部隐去。
bundle := prover.Export(func(i int) bool { return i == 1 || i == 3 })

raw, _ := json.Marshal(bundle)
var received redaction.Bundle
json.Unmarshal(raw, &received)
if err := redaction.Verify(&received); err != nil {
    // 导出物不可信
}
```

`Verify` 的校验顺序：

1. `Total == len(Items)` 且非空，根为 32 字节。
2. 每个槽位 `Items[i].Index == i`，序号 0..N-1 无遗漏、无重复、无重排。
3. 隐去项不得泄露载荷/路径；公开项的承诺必须等于按当前 `logID`、序号、
   载荷重算的值。
4. 用全部承诺（隐去项直接使用、公开项重算）按规范顺序重建整棵树并比对
   最终根，因此隐去承诺也被钉死在其位置上，根同时校验日志 ID 与总条数。
5. 逐条重放公开项的 Merkle 成员路径，路径长度必须等于树深，兄弟哈希
   必须为 32 字节，且收敛到同一 Merkle 根。

## 安全性质（均有测试）

- 遗漏条目：严格序号检查直接拒绝；即使回填 `Total` 也无法通过根校验。
- 重排：位置交换、序号交换、载荷交换分别由序号检查与承诺绑定拒绝；
  全隐去场景交换承诺会因叶子层重建不一致而在根处失败。
- 重复序号：槽位序号检查拒绝。
- 跨日志拼接：承诺与根都混入 `logID`，换根、换个别承诺、整包改 `logID`
  （即使两条日志载荷完全相同）全部失败。
- 篡改载荷/承诺/根/Merkle 兄弟哈希、伪造总条数、隐去项夹带内容、
  截断路径等均返回对应的哨兵错误（可用 `errors.Is` 判别）。

## 测试

```
go test -race -count=1 ./...   # 通过，语句覆盖率 100%
go vet ./...                   # 干净
```

随机化测试覆盖 19 种日志规模（含 n=1 单条目空路径、奇数节点复制层），
每轮 25 个随机公开子集，并额外包含全隐去（`Export(nil)`）与全公开子集。
