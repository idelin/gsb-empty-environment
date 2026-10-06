# prefix-policy-engine

TypeScript 路径前缀权限判定库。

## 判定语义

- 规则作用于**路径前缀**（按路径段匹配：`/a/b` 命中 `/a/b` 与 `/a/b/c`，不命中 `/a/bc`）。
- **越具体优先**：匹配前缀段数更多的规则胜出。
- **同深度拒绝优先**：同一深度下 `deny` 胜过 `allow`；剩余并列按规则 id 字典序确定。
- **无规则默认拒绝**：没有任何规则命中时结果为 `deny`（`defaultDeny: true`）。

## 组继承

`groups` 中 `A.groups: ['B']` 表示 A 继承 B 的成员（B 的成员也是 A 的成员），可传递。
`update` 时检测继承环，**发现环即拒绝整次更新**；其它校验包括规则 id 重复、
未知组引用、非法前缀/效果值。校验全部通过才**原子替换**配置，失败时旧配置继续生效。

## 判定输出

`decide(user, path)` 返回 `Decision`：

- `effective`：实际生效的规则（默认拒绝时为 `null`）。
- `shadowed`：被遮蔽的规则——命中但未决定结果的规则（更浅前缀、同深度被拒
  绝压过的 allow、以及 id 次序落败的并列规则），按优先级排序。
- `matched`：全部命中规则；`effect` / `defaultDeny` 为最终结果。

## 用法

```ts
import { PolicyEngine } from './src';

const engine = new PolicyEngine();
const result = engine.update({
  groups: { eng: { members: ['alice'] }, staff: { groups: ['eng'] } },
  rules: [
    { id: 'r1', subject: 'staff', subjectKind: 'group', prefix: '/repo', effect: 'allow' },
    { id: 'r2', subject: 'alice', subjectKind: 'user', prefix: '/repo/secret', effect: 'deny' },
  ],
});
if (!result.ok) console.error(result.errors);

engine.decide('alice', '/repo/secret/key'); // deny，effective = r2，shadowed = [r1]
```

## 核对模型

`src/reference.ts` 是独立的朴素参照实现（展开全部组到不动点 + 全量扫描前缀匹配），
`test/cross-check.ts` 用确定性随机配置对 `PolicyEngine` 与参照模型做交叉核对，
覆盖**撤销**（更新序列中移除/恢复规则后判定一致）与**同级冲突**（同深度 allow/deny
必为 deny）两类场景，以及继承环拒绝后双方状态保持一致。

## 构建与测试

```sh
npm test   # tsc 编译后运行全部单元测试与交叉核对
```
