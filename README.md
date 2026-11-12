# 权限策略祖先交集

嵌套文档（iframe 树）中的摄像头等能力，由每一层祖先策略与该层自身声明共同限制。本包是一个可导入的
TypeScript 策略树实现：每层给出“功能 → 来源集合”的映射，有效权限沿根到当前节点的路径逐层求交。

## 核心语义

- **逐层求交**：节点对某功能的有效允许列表是根到该节点各层允许列表的交集。任意一层缺失该功能的声明，
  等价于该层不收紧（默认允许任意具名来源），因此未在任何层出现的功能默认放行。
- **两层来源**：每个非根节点同时受本层文档声明（`declared`）与父嵌入容器 `allow` 属性（`container`）
  约束；该节点这一层的允许列表是两者的交集。
- **通配不越层**：`*`（`ALLOW_ALL`）只代表“当前层允许任意具名来源”，不会解除任何上层已经施加的限制；
  通配取交集时按 AND 处理，任一层不是通配，具名来源就必须在该层显式出现。
- **不透明来源**：opaque origin 只凭自身唯一令牌匹配，通配永远不匹配不透明来源；必须在路径上每一层都
  显式携带该令牌才会放行。
- **导航重建子树**：对某浏览上下文调用 `navigate` 会替换其来源与文档声明，并销毁其整个子树；旧节点上已算出的
  能力快照随之失效（函数返回全新树，旧树对象保持不变）。
- **子框架迁移**：`migrateSubtree` 把节点连同其子树挂到新父级，节点自身来源与声明不变，但有效策略按
  新祖先路径与容器策略重新求交，因而可能被新祖先收紧。

## 安装与测试

```bash
npm test
# 等价于：node --experimental-strip-types --test
```

需要 Node ≥ 22（使用原生 TypeScript 类型擦除，无需编译步骤）。

## 用法

```ts
import {
  ALLOW_ALL,
  ALLOW_NONE,
  allowOnly,
  buildTree,
  isAllowed,
  migrateSubtree,
  navigate,
  opaqueOrigin,
  origin,
} from "netsec-20261111-0178";

const top = origin("https://top.test");
const frame = origin("https://frame.test");

const tree = buildTree({
  id: "top",
  origin: top,
  declared: { camera: allowOnly(frame) },
  children: [
    {
      id: "frameA",
      origin: frame,
      container: { camera: ALLOW_ALL },
      declared: { camera: ALLOW_ALL },
    },
  ],
});

const frameA = tree.children[0];
isAllowed(tree, frameA, "camera", frame); // true：顶层点名 + 本层通配
isAllowed(tree, frameA, "camera", origin("https://other.test")); // false：通配解不开上层点名

const secret = opaqueOrigin("opaque-token");
const navigated = navigate(tree, "frameA", secret, { declared: { camera: ALLOW_NONE } });
// frameA 的来源变为不透明来源，且其旧子树被销毁重建

const moved = migrateSubtree(tree, "frameA", "some-other-parent");
// frameA 自身来源/声明不变，但沿新祖先路径重新求交
```

## API 概览

- 来源：`origin(value)`、`opaqueOrigin(token?)`、`sameOrigin`、`originKey`
- 允许列表：`ALLOW_ALL`、`ALLOW_NONE`、`allowOnly(...origins)`、`allowAllPlus(...origins)`、
  `allowlist(wildcard, origins)`、`matches(list, origin)`、`intersect(a, b)`
- 树：`buildTree(config)`、`findNode(root, id)`、`pathTo(root, node)`、`walk(root, visit)`、
  `allFeatures(root)`
- 有效策略：`effectiveAllowlist(root, node, feature)`、`effectivePolicy(root, node)`、
  `isAllowed(root, node, feature, requester)`
- 结构变更：`navigate(root, id, newOrigin, options?)`、`migrateSubtree(root, id, newParentId, options?)`

## 测试策略

`test/tree.test.ts` 用一个三层小树对 7 类允许列表做全枚举（7⁵ 种层组合），核对：

- 有效允许列表等于路径各层的逐层交集；
- 祖先单调收紧——后代放行的来源必被每个祖先放行；
- 不透明来源不被任何通配放行，仅当路径每层都显式携带其令牌；
- 子框架迁移后按新路径重新求交（可被收紧），且旧树不可变；
- 导航销毁重建子树、替换来源与声明，使旧能力失效。
