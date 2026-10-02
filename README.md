# rete-incremental

增量规则 RETE 网络模式匹配器(TypeScript,零依赖)。

## 特性

- **Alpha 网络常量过滤**:每个条件模式的常量约束(`const` 等值 / `cmp` 比较)编译为 Alpha 节点,相同约束的规则共享同一 Alpha 节点与 Alpha 记忆。
- **Beta 网络跨事实 Join**:条件间通过变量(`var`)绑定产生 Join 测试,Join 节点左侧接 Beta 记忆(Token 流)、右侧接 Alpha 记忆(WME 流),逐条增量求积。
- **增量激活**:新增事实沿 Alpha → Join → Beta 记忆 → 终端节点逐层传播,只触碰受影响的节点,绝不重扫事实库。
- **负 Token 精确级联撤回**:每个 WME 记录其派生的全部 Token,每个 Token 记录父子链与所属记忆;撤销事实时沿 `rightUnactivate → deleteToken` 级联精确删除派生 Token,终端节点同步从冲突集移除对应待激活条目。
- **规则热注册**:事实先于规则注入时,新建 Alpha 节点从工作内存一次性播种,Join 节点从左父记忆已有 Token 播种激活。

## 运行环境

- Node.js ≥ 23.6(原生运行 TypeScript,推荐;仓库 `.nvmrc` 锁定 23)
- 或 Node.js ≥ 22.6,运行时加 `--experimental-strip-types`

## 使用

```bash
nvm use          # 切换到 Node 23
npm test         # Node.js 内置测试运行器(node:test)
npm run demo     # 运行验收样例
```

## API 示例

```ts
import { ReteEngine } from './src/index.ts';

const engine = new ReteEngine();

engine.addRule({
  name: 'adult-with-big-order',
  conditions: [
    { type: 'User',  fields: { age: { kind: 'const', value: 25 }, id: { kind: 'var', name: 'uid' } } },
    { type: 'Order', fields: { amount: { kind: 'cmp', op: 'gte', value: 100 }, userId: { kind: 'var', name: 'uid' } } },
  ],
});

const userId  = engine.addFact('User',  { id: 1, age: 25 });
const orderId = engine.addFact('Order', { id: 10, userId: 1, amount: 100 });

engine.getActivations();
// => [{ rule: 'adult-with-big-order', bindings: { uid: 1 }, factIds: [1, 2] }]

engine.retractFact(orderId);   // 负 Token 级联撤回
engine.getActivations();       // => []
```

字段约束三种形式:

- `{ kind: 'const', value }` — Alpha 层等值常量过滤
- `{ kind: 'cmp', op, value }` — Alpha 层比较过滤(`eq/neq/gt/gte/lt/lte`)
- `{ kind: 'var', name }` — 首次出现为变量绑定,再次出现为 Beta 层 Join 测试

## 结构

- `src/rete.ts` — RETE 核心:WME/Token、AlphaNode/AlphaMemory、JoinNode、BetaMemory、TerminalNode、`ReteEngine`
- `src/index.ts` — 公共导出
- `test/rete.test.ts` — `node:test` 测试(含验收场景)
- `examples/demo.ts` — 可运行样例

## 测试结果

```
✔ alpha 网络:常量过滤不通过的事实不会进入网络
✔ beta 网络:跨事实 join,变量不匹配不激活
✔ 验收:注入 User(age=25) 与 Order(amount=100) 后规则增量激活
✔ 验收:撤销 Order 后负 token 精准清除对应冲突集条目
✔ 撤销 User 时沿 token 父链级联撤回全部派生激活
✔ 撤销不存在的事实返回 false 且不影响冲突集
✔ 先注入事实再注册规则:基于 alpha 记忆增量激活,无需重扫事实库
✔ 单条件规则与多规则共享 alpha 节点
ℹ pass 8 / fail 0
```

## 样例输出(`npm run demo`)

```
== 注入 User(age=25) ==
冲突集: (空)

== 注入 Order(amount=100) ==
冲突集: 规则=adult-with-big-order 绑定={"uid":1} 事实=[1,2]

== 撤销 Order(amount=100) ==
冲突集: (空)

User 事实仍存在: true
```
