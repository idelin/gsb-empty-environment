# 协议消息标签歧义

两个握手阶段使用相同编码形状，接收端可能把确认当作授权。本仓库提供可导入的 Go 包
`example.org/netsec/q0470/msglang`，把每个阶段建模为自底向上的有限带标签树自动机
（NFTA），并计算两个阶段消息语言的交集。

## 模型

- 消息是有限带标签树：内部节点是带秩符号（如 `frame`、`body`），叶子是带类型符号。
- 叶子类型 `LeafKind` 含主体 `Principal`、随机数 `Nonce`、字节串 `ByteString`。
- 每个阶段由 `Automaton` 描述：叶子规则 `LeafRule`（可要求精确值，空精确值表示空字段）
  和内部规则 `Rule`，终态集合 `Final` 决定是否接受。
- 交集由乘积自动机 `Intersect` 构造：标签、秩、类型、精确值同步，终态是两个分量终态的配对。

## 核心 API

- `Intersect(left, right)`：返回识别共同语言的乘积自动机。
- `MinimalIntersection(left, right)`：用 Dijkstra 式不动点求共同接受的最小消息树
  （节点数最少，其次高度最小，再按确定性规则顺序），并给出两份解析路径
  `LeftTrace` / `RightTrace`，每棵子树一步自底向上归约。
- `MinimalTree(a)`：单自动机的最小接受树。
- `BoundedIntersect(left, right, bounds)`：有限高度、有限节点预算的树枚举，返回三值判定：
  - `VerdictNonEmpty`：在界内找到见证；
  - `VerdictEmpty`：有限宇宙穷尽且精确不动点判定语言为空；
  - `VerdictUnknown`：节点预算或界先耗尽，绝不把“没枚举完”误报成“空”。

## 歧义与修复

- `AmbiguousAcknowledge` / `AmbiguousAuthorization` 共享相同的 `frame(body(...))` 形状，
  交集非空：同一棵 6 节点树在确认阶段归约到 `ack:frame`，在授权阶段归约到 `grant:frame`，
  这正是“确认被当作授权”的形式化见证。
- `SafeAcknowledge` / `SafeAuthorization` 使用阶段专属的 `ack_body` / `grant_body` 标签，
  乘积语言为空，从根上消除标签歧义。

## 运行

```sh
go test ./...
go run ./cmd/msglang-demo
```

测试用有限高度树枚举验证了空字段（精确空字节叶子）、递归类型（`nil` / `cons` 链）、
预算耗尽返回 `Unknown`、叶子类型与精确值不匹配、以及空语言检测。
