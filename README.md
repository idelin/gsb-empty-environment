# form-state-engine

动态表单状态库：字段可见性/校验可依赖其他字段的**值**与**可见性**，一次修改经全量拓扑重算传播到稳定点。

## 核心语义

- **依赖追踪**：`visibleWhen` / `validateAsync` 通过 `ctx.value(field)`、`ctx.visible(field)` 读取其他字段，读取被自动记录为依赖边，构成有向图。
- **稳定点传播**：每次 `setValue` / `setRules` 后，按拓扑序全量重算可见性直至不动点，随后全量重跑可见字段的同步校验；隐藏字段保留值但不参加校验，其错误立即清除。
- **循环依赖原子拒绝**：`setRules` 先快照，试算发现环（或引用未知字段、规则无法收敛）时整体回滚，返回 `{ ok: false, error }`，旧规则与状态不受影响。构造函数传入成环规则则直接抛 `CycleError`。
- **异步校验版本守卫**：每次 `setValue` 递增字段值版本，`setRules` 递增规则版本。异步结果仅在「运行 ID + 值版本 + 规则版本 + 字段仍可见」全部匹配当前状态时才提交，过期返回一律丢弃。异步校验器读取的依赖字段变化会通过输入签名触发重跑。

## API

```ts
import { FormEngine } from './src/index.ts';

const engine = new FormEngine(['mode', 'email'], {
  email: {
    visibleWhen: (ctx) => ctx.value('mode') === 'advanced',
    validate: (value) => (value ? null : 'required'),
    validateAsync: async (value, ctx) => /* ... */ null,
  },
});

engine.setValue('mode', 'advanced');       // 传播到稳定点
engine.setRules({ /* 整组替换 */ });        // 成环则整体拒绝
engine.getState('email');                  // { value, visible, errors, pending }
engine.dependencies();                     // 当前依赖图
engine.subscribe(() => { /* 变化通知 */ });
await engine.whenSettled();                // 等待所有异步校验完成
```

## 运行测试

需要 Node ≥ 22.6（利用内置 type stripping 直接运行 TypeScript，无需编译）：

```sh
npm test
```

如需类型检查，自行安装 `typescript` 与 `@types/node` 后运行 `npm run typecheck`。

## 目录

- `src/form-engine.ts` — 引擎实现（依赖图、拓扑稳定化、版本化异步提交）
- `test/form-engine.test.ts` — 16 个用例：链式/菱形传播、隐藏保留值、错误消失、环拒绝、规则切换、旧异步返回丢弃等
