# 插件加载器（规划器 + 生命周期模拟）

TypeScript 实现的插件系统，包含依赖规划器与生命周期加载器。

## 功能

- **前置插件（硬依赖）**：`requires` 声明名字与版本范围，规划器做拓扑排序，保证依赖先初始化。
- **可选依赖**：`optional` 存在且兼容则启用并排在宿主之前；缺失或引发冲突则跳过并记录说明，宿主插件保留。
- **冲突范围**：`conflicts` 声明不得共存的版本范围；求解器优先选择能避开冲突的版本，无法避开时拒绝后请求的根插件。
- **最小冲突解释**：基于回溯约束求解 + 删除式极小化（MUS），输出的约束集合中删掉任意一条冲突即消失。
- **保留可独立工作的插件**：某个根被拒绝或初始化失败时，只影响依赖它的部分，其余插件照常进入计划 / 完成初始化。
- **初始化失败级联**：只卸载（逆拓扑序）依赖失败插件的已加载部分；失败插件自身 `dispose` 后标记 `failed`。
- **动态请求去重**：初始化回调里 `ctx.require(name)` 可动态拉取插件——已初始化直接复用实例，进行中复用同一 Promise，并发请求也只初始化一次；循环动态请求会被检测并拒绝。

## 结构

- `src/semver.ts` — 语义化版本与范围匹配（`^`、`~`、`>=`、`1.x`、`||` 等）
- `src/planner.ts` — 规划器：约束求解、版本选择、冲突解释、拓扑排序
- `src/loader.ts` — 生命周期：`loaded → initializing → initialized → started → stopped`，以及 `unloaded` / `failed`
- `src/manifest.ts` — 插件清单类型
- `test/` — `node:test` 测试（规划器 8 项、加载器 6 项、semver 2 项）

## 运行

需要 Node ≥ 23.6（原生类型擦除运行 TypeScript）：

```sh
node --test test/*.test.ts   # 或 npm test
```

类型检查（可选）：`npm i -D typescript @types/node && npx tsc --noEmit`。

## 示例

```ts
import { createPlan } from './src/planner.ts';
import { PluginLoader } from './src/loader.ts';

const plan = createPlan(
  [
    { name: 'app', version: '1.0.0', requires: { core: '^1.0.0' }, conflicts: { legacy: '<2.0.0' } },
    { name: 'core', version: '1.2.0' },
    { name: 'legacy', version: '2.1.0' },
  ],
  [{ name: 'app' }, { name: 'legacy' }],
);
// plan.order: core@1.2.0 → app@1.0.0 → legacy@2.1.0（自动选择避开冲突的版本）

const loader = new PluginLoader(plan, {
  core: { init: () => ({ version: '1.2.0' }) },
  app: {
    init: async (ctx) => {
      const core = await ctx.require('core'); // 已初始化 → 直接复用，不重复 init
      return { core };
    },
  },
  legacy: { init: () => ({}) },
});
const report = await loader.initAll();
// 某个插件 init 抛错时：仅其依赖方被逆序卸载，report.unloaded 记录原因，独立插件保留
```
