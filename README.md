员工终端可持有带签名的部门、地点与资质声明，服务端要判断某项操作是否获准，又不能索取多余属性。给TypeScript决策宿主实现可导入的证明选择库。策略为有序的许可或禁止，条件支持等值、有限集合、否定、AND和OR，最后一条匹配生效。条件可能相关，调用方给出属性域和不可同时成立的组合。未披露只表示未知，终端还可提供签名的集合成员证明而不揭示确值。为当前真实属性选择总代价最小的一组互相兼容的可出具证明，使所有与这些证明一致的合法属性都得到相同授权结论；若多个同价，按披露的确值个数及证明编号序列决胜。返回可独立验证的充分性依据，证明不可再少时须用另一合法赋值说明失效。用小属性域全枚举验证有依据的否定、缺失声明、两个弱证明组合、禁止规则覆写及某声明已撤销。

## 用法

库入口为 `src/index.ts`（Node ≥ 23.6 可直接以类型擦除方式导入 TypeScript）。

```ts
import { selectProofs, verifyEvidence, decide } from './src/index.ts';

const result = selectProofs({
  domain,        // 属性域 + 不可同时成立的组合
  policy,        // 有序许可/禁止规则，最后一条匹配生效
  trueAttributes,// 终端当前真实属性
  proofs,        // 终端持有的签名声明 / 集合成员证明（各自带代价）
  revokedProofIds: ['c-revoked'],
});

if (result) {
  // result.evidence 可交给任何独立验证方：
  const ok = verifyEvidence(domain, policy, result.evidence);
}
```

- `selectProofs` 返回总代价最小、互相兼容且可出具的证明组，使所有与这些证明一致的合法赋值得到相同授权结论；同价时按披露确值个数、证明编号序列决胜；无解返回 `null`。
- `evidence.minimality` 对每个选中证明给出一个合法反例赋值：移除该证明后结论不再唯一，说明证明不可再少。
- `verifyEvidence` 不信任选择器，独立全枚举验证充分性与极小性。

## 测试

```sh
npm test
```
