# B+ 树完整性校验器与全树不变量巡检仪（Go）

数据库自愈与一致性离线巡检工具。`Inspect` 自顶向下遍历整棵 B+ 树，验证全部结构不变量，
并对任何违规输出带根到节点路径的精确定位报告。仅使用 Go 标准库。

## 树约定

内部节点含子指针 `c[0..m]` 与路由键 `k[0..m-1]`，**路由键 `k[i]` 等于左子树 `c[i]` 的最大键**；
查找时下探到第一个满足 `key <= k[i]` 的子树。叶子节点通过 `prev`/`next` 构成双向链表。
所有写操作由 `sync.RWMutex` 保护，支持并发插入/删除。

## 巡检的不变量

| 规则 ID | 检查内容 |
|---|---|
| `KeyOrder` | 每个节点内键严格递增 |
| `Occupancy` | 非根节点键数在 `[MinKeys, MaxKeys]` 内 |
| `ParentPointer` | 父指针与自顶向下路径一致 |
| `FanOut` | 内部节点 `len(children) == len(keys)+1` |
| `RoutingKeyMismatch` | 路由键 `k[i]` 必须等于左子树最大键（捕获"父路由键大于左子树最大键"） |
| `SubtreeRangeOverlap` | 相邻子树键范围不重叠：`max(c[i-1]) < min(c[i])` |
| `LeafChainHead` / `LeafChainCycle` / `LeafChainPrev` / `LeafChainOrder` / `LeafUnreachable` / `LeafChainStranger` | 叶子链表：从最左叶子起始、无环、`prev`/`next` 完全闭环、跨叶有序、与树结构一一对应 |

## 运行

```bash
go test -race -v ./...   # 测试（含并发压力 + 故障注入）
go run ./cmd/inspect-demo # 运行样例
```

## 运行样例输出（`go run ./cmd/inspect-demo`）

```
phase 1: 8 workers x 20000 mixed insert/delete ops in 64ms

phase 2: inspecting tree after concurrent workload
inspection: nodes=34654 leaves=25347 keys=97690 height=9
result: OK - all invariants hold

phase 3: injected fault - routing key at internal node 0x1400045ebd0, keys[1]: 5042847 -> 5042848 (true left-subtree max is 5042847)

phase 4: re-inspecting corrupted tree
inspection: nodes=34654 leaves=25347 keys=97690 height=9
result: CORRUPT - 1 violation(s)
  #1 [root] RoutingKeyMismatch: routing key keys[1]=5042848 is greater than left subtree max 5042847 (subtree at root.children[1])
fault successfully detected and located
```

## 测试结果（`go test -race -v ./...`）

```
=== RUN   TestModelRandomOps            --- PASS (0.11s)   # 2 万随机写删 vs map 模型对拍，期间多次巡检
=== RUN   TestConcurrentStressInspect   --- PASS (0.33s)   # 8 goroutine x 5000 并发写删后巡检
    post-stress inspection passed:
    inspection: nodes=9235 leaves=6779 keys=26165 height=8
    result: OK - all invariants hold
=== RUN   TestDetectRoutingKeyViolation --- PASS (0.00s)   # 注入"父路由键大于左子树最大键"
    injected fault located as:
      #1 [root] RoutingKeyMismatch: routing key keys[1]=324 is greater than left subtree max 323 (subtree at root.children[1])
=== RUN   TestDetectRoutingKeyTooSmall  --- PASS (0.00s)   # 注入路由键偏小
=== RUN   TestDetectLeafChainCycle      --- PASS (0.00s)   # 注入叶子链表环
=== RUN   TestDetectLeafChainPrevBreak  --- PASS (0.00s)   # 注入 prev 指针断裂
=== RUN   TestDetectLeafUnlinked        --- PASS (0.00s)   # 注入叶子脱链
=== RUN   TestEdgeTrees                 --- PASS (0.00s)   # 空树/单键/删空
PASS
ok      bptree
```

另以 `go test -count=5` 与 `go test -race -count=2` 重复运行，全部通过。

## 文件结构

- `bptree.go` — B+ 树实现（插入/删除/分裂/借还/合并，并发安全）
- `inspect.go` — 巡检仪：`Inspect(*Tree) *Report`，违规含路径、规则、键值详情
- `inject.go` — `InjectRoutingKey` 故障注入工具（仅供测试/演示模拟磁盘损坏）
- `bptree_test.go` — 模型对拍、并发压力巡检、各类故障注入检测
- `cmd/inspect-demo/main.go` — 端到端运行样例
