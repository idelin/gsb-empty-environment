# selective-store

从零实现的响应式状态库(不依赖任何现成响应式框架):订阅者只关心**选择器投影**,
通过**可配置相等函数**抑制无变化通知,选择器组成**参数化共享子图**并带缓存身份管理。
纯 TypeScript,零依赖,使用 Node.js 内置测试运行器验证。

## 运行要求

- Node.js >= 22.6(使用内置类型擦除运行 `.ts`,无需编译步骤、无需安装依赖)

```bash
nvm use            # 切到 .nvmrc 指定的 Node 22
npm test           # node --experimental-strip-types --test test/*.test.ts
npm run demo       # node --experimental-strip-types examples/demo.ts
```

## 核心概念

### 选择器图与缓存身份

`defineSelector({ id, compute, equal, equalVersion })` 定义选择器;`def.with(params)`
得到参数化引用。同一个定义是**共享的参数化子图**,不同参数各自拥有缓存实例,互不混用。

缓存身份由三部分共同组成:

- **参数**:`instanceKey = "${id}#eq${equalVersion}#${stableKey(params)}"`
- **相等函数版本**:`equalVersion` 进入缓存键,换版本即换缓存
- **输入依赖**:`graph.cacheIdentity(ref)` 追加依赖实例键及其版本,如
  `proj#eq0#undefined|deps=[root#eq0#undefined@2]`

### 动态依赖与分支切换

`compute(read, params)` 内通过 `read(ref)` 动态建立依赖。每次重算重新收集依赖,
不再被读取的旧依赖会被**撤销**(从被依赖方的 dependents 中移除),条件分支切换后
旧分支的选择器不再被重算。

### 事务与发布

- `store.transaction(tx => ...)` 内可写多次,读者(`getState()`)只能看到**提交前**状态,
  中间态对外不可见;提交时只发布**最终差量**(一轮通知)。
- 通知循环支持**重入写**(监听器内再写会排队到下一轮)与**取消交错**(本轮快照中
  已取消的订阅者被跳过)。
- 冲刷结束后对每个订阅做**独立全量投影参考核验**:绕过缓存从头求值,与增量缓存值做
  结构性比较,不一致计入 `stats.verificationFailures` 并上报 `onError`。

### 相等函数的明确语义

- 每个选择器、每次订阅都可配置 `equal`;投影相等则**不通知**(根对象换引用但投影
  相等也不通知)。
- 相等函数**抛错**:记录 `stats.equalityErrors`、上报 `onError`,对该订阅
  **fail-open**(视为已变化并通知),其余订阅不受影响。

### 不可变性

- 状态更新走路径级结构共享(`setIn`/`unsetIn`/`merge`),提交后的状态与选择器返回值
  默认**深度冻结**(`freeze: false` 可关)。
- 每次提交的操作追加到 `store.opLog`;`replayOps(initial, ops)` 或 `store.replay(ops)`
  可完整**回放**重建状态。

### 统计

`store.stats`:`commits / recomputes / notifications / verifications /
verificationFailures / equalityErrors / listenerErrors`。

## 目录结构

- `src/graph.ts` — 选择器定义、参数化缓存实例、动态依赖、参考求值器
- `src/store.ts` — 事务、订阅、通知冲刷、核验、统计
- `src/ops.ts` — 可回放操作(`set`/`unset`/`merge`)与结构共享更新
- `src/equal.ts` / `src/freeze.ts` — 结构相等、深度冻结
- `test/store.test.ts` — 14 个 `node:test` 用例,覆盖全部需求
- `examples/demo.ts` — 可运行演示
