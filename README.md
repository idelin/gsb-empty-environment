# Ceramic 流冲突解决包

供 TypeScript 节点在流状态更新时调用，在同一流的两条分叉历史之间选出应当保留的当前提示（tip）。

## 解决规则

设节点当前采用的分支为 `current`，新收到的候选分支为 `candidate`。

### 分叉点不早于当前已锚定提交（通用规则）

1. 只有一支带有效锚定时，已锚定的一支胜，未锚定的一支即使更长也不能取代。
2. 两支都带有效锚定时，锚定区块时间戳较早者胜；时间戳相同则历史较长者胜。
3. 两支都未锚定时，历史较长者胜。
4. 长度也相同时，提示 CID 字典序较小者胜。

### 分叉点早于当前已锚定提交（回滚防护）

当候选分支在当前分支的锚定提交之前就已经分叉时，通用规则让位于更严格的约束：

1. 候选分支没有有效锚定时，无论多长都不能取代当前分支。
2. 候选分支的锚定时间戳必须**严格早于**当前锚定时间戳才允许取代；时间戳相等或更晚都拒绝取代，防止用同时间或更长的历史回滚已锚定状态。

### 锚定凭据验证

锚定凭据的验证逻辑由调用方通过 `AnchorVerifier` 注入，本包不内置任何链上/服务端校验。
验证器返回 `undefined`、返回值缺少数字时间戳，或验证过程抛异常，均视为未通过验证，该锚定按未锚定处理。
一条分支上有多个锚定时，以链上最后一个通过验证的锚定作为该分支的锚定。

## 用法

```ts
import { resolveConflict, type AnchorVerifier, type Branch } from './src/index.js';

const verifier: AnchorVerifier = async (proof, commitCid, branch) => {
  const anchor = await verifyAnchorProof(proof); // 调用方自己的校验
  return anchor ? { anchorTimestamp: anchor.blockTimestamp } : undefined;
};

const result = await resolveConflict(currentBranch, candidateBranch, verifier);
// result.winner / result.loser / result.tipCid / result.reason ...
```

主要导出（见 `src/types.ts` 与 `src/resolver.ts`）：

- `resolveConflict(current, candidate, verifier)`：异步返回 `ResolutionResult`，含胜出/落败分支、提示 CID、长度、锚定时间戳与命中规则 `reason`。
- `AnchorVerifier`：`(proof, commitCid, branch) => AnchorInfo | undefined | Promise<...>`。
- `Branch`：`{ genesisCid, commits }`，`commits` 为从创世到提示的有序提交链，每个提交含 `cid`、`prev` 与可选 `anchor`。
- `ConflictResolutionError`：创世 CID 不一致或提交链结构损坏时抛出。

## 设计说明

- CID 在接口层统一表示为字符串，字典序比较即字符串比较；调用方需保证同一 CID 始终使用相同的规范字符串。
- 分叉点通过两支提交链中最后一个同下标且 CID 相同的位置计算。
- 输入分支会做结构校验：提交链非空、首提交与创世 CID 一致且无 `prev`、后续提交的 `prev` 连续、CID 不重复。

## 测试

需要 Node.js >= 22.6（使用内置类型擦除与 `node:test`，无需编译）：

```sh
npm test
```

`test/resolver.test.ts` 覆盖：已锚定压制更长未锚定、同时间戳比长度、分叉点早于锚定的取代/拒绝、伪造锚定按未锚定处理、验证器抛异常、多锚定取最后有效锚定，以及结构错误抛异常等场景。
`test/ts-loader.mjs` 仅供测试使用，把 NodeNext 风格的 `.js` 相对导入解析回 `.ts` 源文件。
