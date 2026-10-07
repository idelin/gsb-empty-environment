# form-state

零依赖的 TypeScript 动态表单状态库。

## 语义

- **字段依赖**:每个字段通过 `deps` 声明依赖,规则(`visibleWhen` / `validate`)经 `RuleContext` 读取其他字段的值与可见性。
- **隐藏字段**:保留 `value`,但不参与校验;隐藏瞬间错误立即清除。
- **稳定点传播**:任何一次修改(`setValue` / `updateRules`)都会同步传播到稳定点 —— 先按拓扑序全量重算可见性,再对所有可见字段全量重算校验。
- **原子规则更新**:`updateRules` 批量应用;若更新后的依赖图存在环(含自环),抛出 `CycleError`,整批更新不生效,旧规则继续有效。
- **异步校验版本守卫**:异步校验启动时捕获字段的校验序号,仅当序号仍匹配(值未变、规则未切换)且字段仍可见时才提交结果;旧校验返回一律丢弃。

## 用法

```ts
import { FormStore } from './src/index.ts';

const store = new FormStore(
  {
    mode: {},
    secret: {
      deps: ['mode'],
      visibleWhen: (ctx) => ctx.value('mode') === 'advanced',
      validate: async (value) => (value ? null : 'required'),
    },
  },
  { mode: 'basic' },
);

store.setValue('mode', 'advanced'); // 同步传播:secret 变为可见并开始校验
await store.whenSettled();          // 等待在途的异步校验
store.snapshot('secret');           // { value, visible, errors, validating }
```

## 运行测试

需要 Node >= 22.6(`--experimental-strip-types`;Node >= 23.6 默认开启):

```sh
npm test
```

## 结构

- `src/graph.ts` — 依赖图的环检测(`findCycle`)与拓扑排序(`topoOrder`)。
- `src/store.ts` — `FormStore`:稳定点传播、原子规则更新、版本守卫的异步提交。
- `src/types.ts` — 公共类型(`FieldRules`、`RuleContext`、`FieldSnapshot` 等)。
- `test/store.test.ts` — 覆盖规则切换、旧校验返回、错误消失、环拒绝、链式传播等场景。
