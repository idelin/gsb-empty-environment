# itc — Interval Tree Clock（区间时间戳 / 无中心逻辑时钟）

Go 标准库实现的 Interval Tree Clock（Almeida, Baquero, Fonte, 2008），
面向**动态拓扑**的无中心因果跟踪：

- 无需预先设定固定集群节点列表，节点可随时加入/退出；
- 通过递归划分时间区间 `[0, 1]` 进行 `Fork`（分叉）与 `Join`（合并）；
- 零配置跟踪因果关系：可判定两个时钟的 先后 / 并发 关系。

## 核心概念

每个时钟（`Stamp`）由两棵同构的二叉树组成：

- **ID 树**：表示该时钟拥有时间区间 `[0,1]` 的哪一部分。`1` 表示整个区间，
  `(1,0)` / `(0,1)` 表示左/右半区间，可递归细分。
- **Event 树**：与 ID 树结构对应的计数器树，记录区间内各处发生的事件数。

| 操作 | 含义 |
| --- | --- |
| `Seed()` | 创建持有完整区间 `[0,1]` 的初始时钟（每个因果域仅一次） |
| `Fork()` | 裂变为两个各持一半区间的子时钟（新节点加入） |
| `Join(a, b)` | 合并两个持有不相交区间的时钟（节点退出/状态同步） |
| `Event()` | 记录一次本地事件，推进时钟 |
| `Peek()` | 生成匿名只读快照（可比较、不可记录事件），用于附在消息上 |
| `Compare(a, b)` / `Leq` | 判定因果先后：`Less` / `Equal` / `Greater` / `Concurrent` |

## 运行

```sh
go test ./...        # 运行测试
go run ./cmd/itc-demo  # 运行样例
```

## 样例输出

```
=== Interval Tree Clock demo ===

[1] seed (full interval [0,1]):
    seed = {id: 1, event: 0}

[2] fork into two halves:
    A = {id: (1, 0), event: 0}
    B = {id: (0, 1), event: 0}

[3] concurrent events (A:2, B:1):
    A = {id: (1, 0), event: (0, 2, 0)}
    B = {id: (0, 1), event: (0, 0, 1)}
    Compare(A, B) = concurrent

[4] join A and B:
    J = {id: 1, event: (1, 1, 0)}
    Compare(A, J) = less (happened-before)
    Compare(B, J) = less (happened-before)
...
```
