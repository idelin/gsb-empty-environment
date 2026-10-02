# gesture-arbiter

从指针事件流中识别 **click / drag / pinch** 三种互斥手势的纯 TypeScript 实现。
零依赖、不借浏览器布局引擎：节点树与字形度量全部来自夹具（fixture），
用 Node.js 内置测试运行器（`node:test`）验证。

## 运行要求

Node.js ≥ 22.6（原生 TypeScript 类型擦除；建议 ≥ 23.6，无需任何 flag）。
仓库零依赖，无需 `npm install`。

```sh
node --test            # 运行全部测试
node examples/demo.ts  # 运行样例，打印事件流与识别输出
```

## 设计

### 状态机与仲裁（`src/recognizer.ts`）

仲裁按 **指针集合 + 手势代际（generation）** 维护：每次离开 `idle` 开启新一代，
所有输出携带 `gen`；同一代内**至多一个 `commit`**，因此互斥手势不可能同时提交。

```
idle ──down──> possible ──dist>slop──> drag ──up──> commit:drag
                │  │                    │down#2: compensate+cancel drag
                │  └──up: dist≤slop 且 dur≤timeout → commit:click
                │                     └──────────> pinch ──up──> commit:pinch
                └──down#2: cancel click ─────────┘   （剩余手指转为 inert，不会复活成 click）
```

- **阈值**：`tapSlop=10px`（`dist > slop` 才升级为 drag，等于阈值仍是 click）、
  `tapTimeout=300ms`（`dur ≤ timeout` 才算点击）。轨迹生成器专门枚举 `slop±1`
  与 `timeout±1` 的边界事件。
- **预览与补偿**：drag/pinch 进行中发出增量 `preview`；手势被取代（第二指加入）
  或指针丢失（`cancel`/`lostcapture`）时发出 `compensate`，精确抵消已发预览
  （`Σdx=0`、`Πscale=1`），消费方可无损回滚。
- **取消与捕获丢失**：`cancel`/`lostcapture` 移除指针；指针集合清空 ⇒ 代际结束、
  回到 `idle`，丢失的指针**永远不会让手势永久保持激活**。
- **延迟/乱序事件**：时间戳倒退、未知 pointerId、重复 down、settled 后的 down
  一律返回 `ignored`，不影响会话。
- **坐标系变化**：事件携带 `epoch`；epoch 变化时按当前位置重新基线化
  （re-baseline），不产生跳变增量；pinch 在双指 epoch 对齐前暂停缩放预览。

### 有限轨迹生成器 + 独立检查器（`src/tracegen.ts` / `src/checker.ts`）

生成器确定性地枚举 43 条轨迹：阈值边界、第二指加入时机（slop 前/后、超时后）、
捕获丢失、坐标系切换、延迟/杂散事件的交错。检查器**不依赖识别器内部状态**，
只消费公开输出流与 `isIdle()`，验证：单事件至多一次 commit、每代至多一次 commit、
commit 总量 == 预览累加、补偿精确平衡、取消前必先补偿、关闭的手势不再发声、
轨迹结束必须回到 idle。

### 节点树与字形度量（`src/layout.ts` / `src/fixtures.ts`）

文本宽度由手写字形夹具（unitsPerEm=1000 的 advance 表）计算，命中测试在纯数据
节点树上做最深包含查询——核心计算不借浏览器布局引擎。`hitTest` 结果作为
`targetId` 挂到 commit 上。

## 运行样例（`node examples/demo.ts` 节选）

```
== second finger joins mid-drag -> drag compensated, pinch commits ==
-> move#1 (124,350) t=60 epoch=0
  <- preview drag  gen=3 dx=24.0 dy=0.0 scale=1.000
-> down#2 (220,350) t=120 epoch=0
  <- COMPENSATE drag gen=3 dx=-24.0 dy=0.0 scale=1.000
  <- cancel drag gen=3 (superseded-by-pinch)
-> move#2 (250,350) t=180 epoch=0
  <- preview pinch  gen=3 dx=15.0 dy=0.0 scale=1.313
-> up#1 (104,350) t=300 epoch=0
  <- COMMIT pinch gen=3 dx=5.0 dy=0.0 scale=1.521 dur=300ms target=dialog
```

## 测试结果

```
$ node --test
✔ 16 个测试全部通过（11 个识别器单元测试 + 3 个轨迹属性测试 + 2 个布局测试）
ℹ tests 16   ℹ pass 16   ℹ fail 0
```

覆盖：click 阈值边界（slop/timeout ±1）、click→drag 不再发 click、
第二指加入切 pinch（含 drag 补偿）、pinch 缩放比例、cancel/lostcapture
补偿与终止、坐标系 re-baseline、延迟/乱序/杂散事件忽略、43 条枚举轨迹的
合法状态转移与提交计数。
