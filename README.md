# Spec-Driven Interaction Runtime

从声明式规约生成**交互执行器**与**独立模型检查器**，核心布局计算纯函数完成——
节点树与字形度量由夹具（fixtures）给出，不借助浏览器布局引擎。

- 语言：TypeScript（Node.js ≥ 23.6 原生类型擦除，直接运行 `.ts`，无需编译步骤）
- 测试：Node.js 内置测试运行器（`node:test`），无第三方依赖

## 结构

| 文件 | 职责 |
| --- | --- |
| `src/spec.ts` | 规约类型：层级状态（mode 树）、事件表（守卫 + 原子多字段动作）、不变量、不可达谓词、确定性状态哈希 |
| `src/executor.ts` | 由规约生成的执行器：分发、不变量检查、事件录制/回放、副作用事务回滚、带迁移证据的热换规约 |
| `src/checker.ts` | 独立模型检查器：自实现一步语义，有限输入域上 BFS，返回最短反例 |
| `src/crosscheck.ts` | 执行器轨迹与检查器一步语义逐步交叉核验 |
| `src/layout.ts` | 纯布局核心：贪心断行、文档堆叠、光标几何（无 DOM） |
| `src/fixtures.ts` | 夹具：节点树与字形度量 |
| `src/editorSpec.ts` | 具体规约：多段落编辑器（层级模式 `active.insert` / `active.select` / `readonly` / `dialog.confirm`） |
| `src/main.ts` | 运行样例 |

## 语义要点

- **非法事件保留诊断不改状态**：未知事件 / 目标节点缺失 / 模式不匹配 / 守卫拒绝 /
  不变量违反 / 副作用失败，全部记入诊断日志，状态保持原值。
- **原子多字段动作**：如 `TYPE` 一次性更新文本树、光标与布局缓存，全部算出后才提交。
- **副作用回调失败按事务约定回滚**：effects 在提交前执行，任一抛错则整个事件回滚。
- **回放分歧**：回放中事件被拒绝（如目标节点缺失）会记录为 divergence。
- **确定性**：相同初态 + 相同事件序列 ⇒ 相同状态哈希与轨迹。
- **热换规约**：迁移函数产出新状态后，必须提供迁移证据（新模式合法 + 新不变量全部成立），
  否则保留旧规约与旧状态。
- **模型检查**：有限输入域上 BFS（按状态哈希去重），首个反例即为最短事件前缀。

## 运行

```bash
npm run demo   # node src/main.ts
npm test       # node --test 'test/*.test.ts'
```

## 运行样例（`npm run demo` 摘要）

```
=== 1. executor dispatch + diagnostics ===
ok      {"type":"MOVE","payload":{"to":11}}
ok      {"type":"TYPE","payload":{"ch":"!"}}
REJECT  {"type":"TYPE","payload":{"ch":"中"}}
...
diagnostics:
  #3 [guard-rejected] no glyph metric for '中'
  #6 [mode-mismatch] event 'TYPE' not allowed in mode 'readonly'
  #11 [effect-failed] rolled back: simulated persistence failure
mode=active.insert focus=p1 cursor=2 maxWidth=60
side effects committed: 10 (RESIZE 40 rolled back)

=== 2. pure layout from fixtures ===
p1 text: 'hello world' laid out at maxWidth=60
  line 0: [0,5) width=27px y=0px
  line 1: [6,11) width=30px y=18px
cursor at x=14px y=0px (line 0)

=== 3. record + replay ===
recorded 9 accepted events
replay divergences: 0
replay final hash equals live hash: true

=== 4. model checker (finite domain, BFS => shortest counterexample) ===
ok=true explored=92811 states up to depth 6

=== 5. executor trace vs checker one-step semantics ===
ok=true steps=9 mismatches=0

=== 6. hot-swap spec ===
broken swap ok=false error=migration evidence failed
spec still: editor-v1, cursor still: 2
good swap ok=true evidence modeValid=true invariants=true
spec now: editor-v2
```

## 测试结果（`npm test`）

```
ℹ tests 21
ℹ pass 21
ℹ fail 0
```

覆盖：

- `test/executor.test.ts` — 非法事件诊断且不改状态；不变量违反拒绝；录制/回放确定性；
  回放缺目标节点报分歧；副作用失败事务回滚
- `test/checker.test.ts` — 不变量与不可达状态验证；被破坏规约的最短反例
  （`unreachable-reached` 长度 3、`invariant-violated` 长度 2）；一步语义拒绝非法事件
- `test/crosscheck.test.ts` — 执行器轨迹与检查器一步语义交叉核验；篡改轨迹可检出
- `test/hotswap.test.ts` — 迁移抛错 / 证据失败 / 未知模式均保留旧规约与状态；成功热换
- `test/layout.test.ts` — 贪心断行、长词硬断、段落堆叠、光标几何、布局确定性
