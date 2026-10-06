# prefix-policy-engine

TypeScript 路径前缀权限判定库：组继承、原子配置更新、生效/遮蔽规则输出，
并附带一个"展开全部组 + 朴素前缀匹配"的独立参考模型用于交叉核对。

## 判定语义

- 规则按**路径前缀**匹配（按段匹配：`/a` 匹配 `/a` 与 `/a/b`，不匹配 `/ab`）。
- **越具体优先**：匹配路径的最深前缀规则胜出。
- **同深度拒绝优先**：同一前缀同时存在 allow/deny 时 deny 胜出（同级冲突）。
- **无规则默认拒绝**：没有任何规则匹配时判定为 deny。
- 前缀会被规范化（补前导 `/`、合并重复分隔符、去尾部 `/`），大小写敏感。

## 组继承

- 每个组可声明 `inherits: [...]`，生效规则 = 自身规则 + 递归继承的规则。
- 继承顺序确定：自身规则优先，然后按 `inherits` 声明顺序深度优先展开；
  菱形继承中同一条规则只展开一次。
- **继承环（含自继承）或引用未知组会拒绝整个配置更新**。

## 原子更新

`engine.update(config)` 先完整校验并构建新状态，全部通过后才一次性替换；
校验失败时返回 `{ ok: false, error }`，旧配置完全不受影响（自动撤销）。

## 生效规则与遮蔽规则

`engine.explain(group)` 返回：

- `effective`：实际生效的规则（含来源组与声明下标）。
- `shadowed`：永远不可能胜出的规则，含遮蔽原因：
  - `deny-wins-same-prefix`：同前缀的 allow 被 deny 遮蔽；
  - `duplicate`：同前缀同效果的重复规则，仅离查询组最近的一份生效。

## 参考模型交叉核对

`src/model.ts` 提供独立实现 `expandAll` / `decideFlat` / `shadowedFlat`
（展开全部组后朴素扫描）。`test/model.test.ts` 用随机配置对两个实现做
交叉核对，覆盖：判定一致性、同级冲突、拒绝更新的回滚（撤销）以及
回退到历史配置后判定可复现。

## 用法

```ts
import { PolicyEngine } from './src/index.ts';

const engine = new PolicyEngine({
  groups: {
    base: { rules: [{ prefix: '/data', effect: 'allow' }] },
    analyst: {
      inherits: ['base'],
      rules: [{ prefix: '/data/secret', effect: 'deny' }],
    },
  },
});

engine.decide('analyst', '/data/public').effect; // 'allow'
engine.decide('analyst', '/data/secret/x').effect; // 'deny'
engine.explain('analyst'); // { effective: [...], shadowed: [...] }
```

## 运行测试

需要 Node ≥ 22.6（原生类型擦除运行 TypeScript，推荐 ≥ 23.6）：

```sh
node --test test/*.test.ts   # 或 npm test
```

零运行时依赖；`tsconfig.json` 仅供编辑器与可选的 `tsc` 类型检查使用。
