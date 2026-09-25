# json-tree-diff

Computes a **minimal, deterministic patch** between two JSON-compatible trees
whose nodes carry stable `id` strings, and applies it back. Pure TypeScript,
zero dependencies, tested with the Node.js built-in test runner (Node >= 23
runs `.ts` directly via type stripping).

## Model

- A **node** is an object with a string `id`. The root must be a node.
- An array-valued property whose elements are all node objects is a **child
  slot** (ordered). Everything else is an **attribute**: object key order is
  ignored, array order is significant.
- Nodes are matched between trees by `id`, so identity survives moves.

## Operations

| op       | fields                                   | meaning                          |
| -------- | ---------------------------------------- | -------------------------------- |
| `insert` | `id parentId key index node`             | create node (`parentId: null` = root) |
| `move`   | `id parentId key index`                  | relocate node, identity kept     |
| `remove` | `id`                                     | delete node and its subtree      |
| `update` | `id path value` / `id path unset:true`   | leaf-level attribute set/unset   |

Guarantees:

- **Minimal**: only ids that changed get ops; per slot, children in the
  longest-already-ordered subsequence (LIS) never move; a removed subtree
  costs a single `remove` for its topmost node.
- **Safe order**: updates, then inserts/moves (parents always before their
  children, no dangling references), then removes — so a child moved out of a
  deleted parent is relocated first and never removed twice.
- **Deterministic**: object key order in the inputs never affects the patch;
  `JSON.stringify(diffTrees(a, b))` is stable under key shuffling.
- **Located errors**: invalid input (duplicate id, missing id, non-JSON
  value) and inapplicable ops throw `TreeDiffError` with a JSON Pointer
  (`/children/1`) or op path (`/ops/0`).

## Usage

```ts
import { diffTrees, applyPatch } from './src/index.ts';

const ops = diffTrees(oldTree, newTree); // Operation[]
const result = applyPatch(oldTree, ops); // deep-equals newTree
```

## Run

```sh
npm test    # node --test test/*.test.ts
npm run demo
```
