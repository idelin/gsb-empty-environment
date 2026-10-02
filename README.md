# Dotted Version Vectors (DVV) 精确因果跟踪器

面向无主（Leaderless）复制的 DVV 去重优化算法实现（TypeScript，零依赖）。
每次变更表示为 **点状事件 `(NodeID, Counter)` + 前置向量**，精确区分
「相同值的重复写入」与「不同值的并发覆盖」，消除传统版本向量的幽灵分支。

## 核心设计

- `DottedEvent`（[src/dvv.ts](src/dvv.ts)）：一次写入 = 点状版本标识 `dot` + 前置向量
  `context`（写入者已知的全部因果历史，不含自身的点）。
- `KeyState`（dvvset）：活跃兄弟分支列表（每个分支由自己的点标识）+ 共享前置向量；
  历史事件被压缩为每节点一个计数器，体积为 O(节点数) 而非 O(写入次数)。
- `Replica`（[src/replica.ts](src/replica.ts)）：`write` 分配新点并折叠所有已见兄弟；
  `receive` 幂等地应用乱序/重复事件；`sync` 做双向反熵合并。
- 去重优化（`dedupValues`）：值相同的并发/重复写入折叠为一个确定性的代表分支
  （最小点序），被折叠的点并入前置向量 —— 相同值不再产生冗余兄弟；
  值不同的并发写入保留为真实兄弟分支，由应用层解决后写回坍缩。

## 运行

需要 Node.js ≥ 22.6（推荐 ≥ 23.6，原生运行 TypeScript，无需编译与依赖安装）：

```sh
node --test        # 运行测试
node demo/demo.ts  # 运行样例
```

## 样例输出

```
=== 场景 A：热点键高频写入 + 多轮乱序/重复广播 ===
总写入 900 次（空更新 619，覆盖更新 281）
三副本状态收敛: YES
  n1: values=[693] liveDots=[n1#298] context={n1:297 n2:307 n3:295}
  n2: values=[693] liveDots=[n1#298] context={n1:297 n2:307 n3:295}
  n3: values=[693] liveDots=[n1#298] context={n1:297 n2:307 n3:295}
版本图体积: 4（活跃点 1 + 前置向量 3 项）
对比：逐事件保留分支的朴素方案将保留 900 个分支

=== 场景 B：网络分区下的并发写 —— 同值去重 vs 异值兄弟 ===
分区恢复后（未解决冲突）:
  A: values=["v2","v3"] liveDots=[A#1, C#1] context={B:1}
  B: values=["v2","v3"] liveDots=[A#1, C#1] context={B:1}
  C: values=["v2","v3"] liveDots=[A#1, C#1] context={B:1}
  => 相同值的两个并发写被去重为 1 个分支；不同值保留为真实兄弟，无幽灵分支
冲突解决写回后:
  A: values=["v3-resolved"] liveDots=[A#2] context={A:1 B:1 C:1}
  ...
```

## 测试结果

`node --test`（Node.js 内置测试运行器，10 个用例全部通过）：

```
ℹ tests 10
ℹ pass 10
ℹ fail 0
```

覆盖：版本向量运算、顺序覆盖写无幽灵分支、高频空更新体积恒定、
同值并发写去重、异值并发写保留兄弟并坍缩、乱序+重复广播收敛、
重复投递幂等、计数器复用不变式校验、反熵与事件广播殊途同归、
验收场景（400 轮高频混合写入 + 乱序广播后三副本逐字节一致，版本图 ≤ 1 + 节点数）。
