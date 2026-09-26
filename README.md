# spec-executor-checker

从「状态与事件表」规约生成交互执行器与独立模型检查器。纯 TypeScript，零依赖，
利用 Node.js ≥ 23.6 的原生类型擦除直接运行 `.ts`，测试基于 Node 内置 `node:test`。

## 能力总览

- **执行器**（`src/executor.ts`）：层级状态 + 守卫 + 原子多字段动作；每次迁移后做不变量检查；
  非法事件（无迁移 / 守卫失败 / 不变量违反）不改状态、保留诊断；全程录制日志（含状态哈希）。
- **录制与回放**：相同初态 + 相同事件序列 => 相同结果（逐条哈希比对）；回放中缺目标节点报分歧。
- **事务约定**：副作用回调（effects）任一失败或未注册，整个事件事务回滚到事件前状态。
- **独立模型检查器**（`src/checker.ts`）：独立实现的一步语义；在有限输入域上 BFS，
  不变量违反返回**最短反例**；不可达状态给出证明或最短到达轨迹。
- **交叉核验**（`src/crosscheck.ts`）：执行器轨迹逐步用检查器一步语义独立重算并比对。
- **热换规约**：`hotSwap` 要求迁移函数产出证据（路径结构合法 + 新不变量成立），
  失败则保留旧规约与状态。
- **夹具布局**（`src/layout.ts` + `src/fixtures.ts`）：节点树与字形度量由夹具给出，
  文本测量与盒布局为纯函数计算，不借浏览器布局引擎。

## 结构

```
src/spec.ts        规约类型、编译、路径进入/校验、稳定序列化
src/executor.ts    执行器：分发/不变量/录制/回放/事务回滚/热换
src/checker.ts     独立模型检查器：BFS、最短反例、不可达证明
src/crosscheck.ts  执行器轨迹 vs 检查器一步语义交叉核验
src/layout.ts      纯函数文本测量与盒布局
src/fixtures.ts    字形度量与节点树夹具
src/editorSpec.ts  示例规约（迷你编辑器 v1/v2 + 迁移函数）
samples/demo.ts    端到端运行样例
test/*.test.ts     Node 内置测试（28 个）
```

## 运行

```sh
npm test          # node --test（28 个测试全部通过）
npm run sample    # 端到端样例
```

## 运行样例（`npm run sample` 实际输出）

```
== 1. executor ==
  #0 focus  applied  hash=1276e7cf3afff86a
  #1 type   applied  hash=379bbca2409d199f
  #2 type   applied  hash=e4f1ff664da884e0
  #3 type   applied  hash=23aeed4bf64f50a9
  #4 type   rejected <- guard-failed  hash=23aeed4bf64f50a9
  #5 save   applied  hash=6a50145d2d307b99
  #6 lock   applied  hash=6703db5736eb3cd9
  #7 focus  rejected <- no-transition  hash=6703db5736eb3cd9
  #8 unlock applied  hash=106064b0f3eef1d2
  final: {"path":["root","active","idle"],"data":{"text":"abc","lines":3,"saved":true,"lockCount":1}}
== 2. replay ==
  consistent replay: {"finalHash":"106064b0f3eef1d2","divergences":[]}
  missing-target replay: [{"seq":5,"reason":"target node \"btn-ok\" not found in node tree"}, ...]
== 3. model checker ==
  shortest counterexample: {"invariant":"n-at-most-2","length":3,"events":["inc","inc","inc"],"final":{"n":3}}
  editor invariants: "hold"
  editor unreachable states: {"unreachable":[],"truncated":false}
  editor reachable space: {"states":274}
== 4. cross-validation ==
  crosscheck: {"steps":6,"mismatches":[]}
== 5. hot swap ==
  v1->v2: {"ok":true,"evidence":{"path":["root","active","idle"],"data":{...,"readonly":false}}}
  bad migration: {"ok":false,"errors":["unknown state: nonexistent"]}
  spec still: {"name":"editor","version":2,"hash":"a90dd618b69eee4c"}
== 6. fixture layout ==
  dialog @(0,0) 110x112
    title @(12,12) 45x22
    status @(12,42) 64x20
    buttons @(12,70) 86x30
      btn-ok @(16,74) 20x22
      btn-cancel @(44,74) 50x22
```

## 测试结果（`npm test` 实际输出）

```
✔ executor      7/7   迁移应用、非法事件诊断、守卫拒绝、确定性、副作用回滚、日志哈希链
✔ replay        4/4   录制一致回放、缺目标节点报分歧、无录制回放、回放确定性
✔ checker       7/7   最短反例（长度 3）、可靠规约无反例、不可达状态、最短到达轨迹、有限空间枚举
✔ crosscheck    2/2   执行器轨迹与检查器一步语义一致、交叉核验确定性
✔ hotswap       4/4   证据通过热换、失败保留旧规约与状态、违反新不变量拒绝、迁移抛错拒绝
✔ layout        4/4   夹具字形测量、嵌套布局几何、确定性与原点、节点 id 集合

ℹ tests 28
ℹ pass 28
ℹ fail 0
```

## 设计要点

- **层级状态**：迁移的 `from` 命中活动路径任意祖先，取最深（最具体）者；`to` 沿 `initial` 下沉。
- **原子多字段动作**：`action` 返回部分字段补丁，先应用到副本、过不变量后一次性提交。
- **有限输入域**：`spec.domains`（数据字段域）与 `spec.eventDomains`（事件负载域）
  约束检查器的建模空间，BFS 按层展开保证反例/到达轨迹最短。
- **独立性**：检查器的 `step`/`rawStep` 不依赖执行器任何函数，交叉核验才有意义。
