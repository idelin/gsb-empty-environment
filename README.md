# Ceramic流提示冲突与锚定优先

Ceramic 流的历史可以分叉，节点要在分叉间选出当前提示，攻击者利用泄露或已撤销的密钥另起一支，或谎称更长的未锚定历史来取代已锚定历史。给TypeScript节点写供流状态更新调用的冲突解决包，以下按本题规定：两支从同一祖先分叉时，已锚定的一支优先于未锚定的一支；两支都已锚定时，较早锚定区块时间戳者胜，相同则较长者胜；都未锚定时较长者胜；长度也相同时提示 CID 字典序较小者胜；分叉点早于当前已锚定提交时，新支必须自己的锚定早于当前锚定时间才可取代；锚定凭据验证由调用方注入，未通过验证的锚定当作未锚定。用已锚定与更长未锚定竞争、同时间戳、分叉点早于锚定、伪造锚定验证。

## 安装与运行

包只依赖 Node.js 内置能力，入口为 `index.ts`：

```sh
npm test
```

## 快速使用

分支用根到提示的提交日志表示。锚定凭据放在锚定提交的 `anchor` 字段中，验证函数由节点注入：

```ts
import { resolveConflict } from './index.ts'

const result = resolveConflict(currentState, incomingState, {
  verifyAnchor(proof, commit, context) {
    return anchorService.isValid(proof, context.side)
  },
})

if (result.winner === 'incoming') {
  streamState = result.selected
}
```

`resolveConflict` 返回裁决结果，其中：

- `winner` 为 `'current' | 'incoming'`。
- `selected` / `state` / `winningState` 是可直接用于流状态更新的获胜状态。
- `tip` 是获胜提示 CID。
- `reason` 是本次裁决命中的规则。
- `current` / `incoming` 暴露分支长度、锚定位置与区块时间戳，便于审计。

只想拿状态时可使用 `chooseStreamState`（别名 `selectTip`、`resolveStreamState`、`pickTip`）。验证需要异步 I/O 时使用 `resolveConflictAsync`。也可以把验证函数直接作为第三个参数传入。

## 裁决顺序

1. 比较两支的最长公共前缀，确定分叉点。
2. 注入的 `verifyAnchor` 验证每个锚定凭据；返回 `false`、抛错或异步验证失败时，该锚定按未锚定处理。
3. 分叉后已锚定的分支优先于未锚定分支（即使未锚定分支更长）；分叉前共享的锚定不算作任一支的新锚定。
4. 两支都已锚定时，较早的锚定区块时间戳获胜；时间戳相同则较长分支获胜。
5. 若分叉点早于当前分支的锚定提交，竞争分支必须拥有分叉后的独立锚定，且其时间戳严格早于当前锚定；否则当前分支保留。
6. 两支都未锚定时较长分支获胜。
7. 长度相同时，提示 CID 字典序较小者获胜；完全相同时保留当前状态，保证裁决确定。

一支如果是另一支的直接后代，则视为同一分支的线性延伸，较新提示获胜，不会按“分叉”处理。

## 紧凑状态

没有完整日志时，也可以传入

```ts
{ tip, length, anchored, anchorTimestamp, anchorIndex }
```

并通过选项 `commonPrefixLength`（或 `forkIndex`）提供分叉位置。调用方声称已锚定的紧凑状态视为已由调用方验证；若同时提供锚定凭据，仍会经过注入的验证函数。
