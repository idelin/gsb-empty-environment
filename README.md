# weakbisim — 弱互模拟检查器

比较两个同时含可见动作与内部动作（`tau`）的有限转换系统（每个至多 30 个状态），
计算**最大弱互模拟关系**；不等价时给出**可重放的区分过程**。

## 构建与运行

```sh
go build -o weakbisim .
./weakbisim -a examples/direct.txt -b examples/tau_wrapped.txt   # 等价
./weakbisim -a examples/ab_ac.txt -b examples/a_bc.txt           # 不等价 + 区分过程
go test ./...
```

## 输入格式

```
<numStates> <startState>     # 首个非注释行；numStates <= 30
<from> <label> <to>          # 每行一条迁移
```

标签 `tau`（或 `τ`）为内部动作，其余均为可见动作。`#` 开头与空行被忽略。

## 设计要点

- **弱迁移**：`s =a=> t` 定义为 `tau* · a · tau*`；`a = tau` 时即 `tau*`（零步或多步）。
  τ 闭包用带访问标记的 BFS 计算，**内部循环不会死循环、也不算可见进展**
  （纯 tau 自环系统与死锁弱等价，见 `TestDivergenceEqualsDeadlock`）。
- **最大关系**：从全关系出发做最大不动点迭代，按轮次快照删除不满足弱转移
  条件的 pair，并记录每个被删 pair 的删除轮次（rank）与见证（无法被匹配的
  攻击方迁移）。
- **区分过程**：从不等价初始 pair 出发，每轮攻击方走见证迁移，防守方以弱迁移
  回应且结果 pair 的 rank 严格下降，因此有限步内必然以防守方"卡死"
  （无任何匹配弱迁移）结束。`Replay` 会把整个过程对着两个 LTS 重新校验：
  攻击方每步必须是真实迁移，防守方路径必须是合法的 `tau*·a·tau*`，
  最后的"卡死"必须属实——篡改会被拒绝（见 `TestReplayRejectsTampering`）。
- **直接转换 ≡ 内部步包围的转换**：`TestDirectTransitionVsTauWrapped` 与
  `TestWrappedChainEquivalent` 验证 `s -a-> t` 与 `s -tau-> · -a-> · -tau-> t`
  弱互模拟等价。

## 文件

- `lts.go` — LTS 表示、τ 闭包、弱迁移、弱路径重建、输入解析（30 状态限制）
- `bisim.go` — 最大弱互模拟（最大不动点 + rank/见证）
- `counterexample.go` — 区分过程提取（`Extract`）与重放校验（`Replay`）
- `main.go` — CLI：打印最大关系、判定结果、区分过程并重放验证
- `bisim_test.go` — 上述全部性质的测试
- `examples/` — 示例系统
