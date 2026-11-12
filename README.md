# npm 导出条件差分网关（conditional-diff-gate）

依赖包 `package.json` 的 `exports` 支持自定义条件。恶意依赖可把未经审查的入口
挂在 `development`、`deno` 等自定义条件下，并放在 `default` 之前；当构建带了用户
附加条件时，该入口会被优先选中。本包对同一说明符解析两次并做差分裁决，是一个
零依赖、可导入的 TypeScript 网关。

## 解析语义

- 条件对象按**对象自身键顺序**取第一个处于活动集合内的键。
- 嵌套条件对象无匹配键时，**回溯到上层的下一个键**继续尝试。
- 条件命中 `null` 即**封锁**，立即终止，不回溯、不落到 `default`。
- 支持字符串、`null`、条件对象、数组回退、子路径映射（精确键、`*` 通配、尾部 `/`）。
- 拒绝解析到包目录之外（`..` 逃逸）的目标。

## 差分裁决流程

1. **基线解析**：仅使用调用方给定的基线条件（如 `import`、`node`、`default`）。
2. **附加条件过滤**：用户传入的附加条件，凡不在调用方 `allowedConditions`
   允许集合内的，一律视为未传入（记录到 `ignoredConditions`）。
3. **增强解析**：基线条件 + 通过过滤的附加条件，再解析一次。
4. **入口比对**：
   - 两次入口相同 → 放行（`allow-same-entry`），仍校验文件清单。
   - 两次入口不同 → 新入口必须命中调用方登记的已审查文件 **SHA-256 表**，
     且实际文件哈希一致，否则拒绝。
5. **清单约束**：被选中的入口必须存在于包的文件清单（`manifest`）内，否则拒绝。
6. **归因输出**：入口改变时，输出是哪个附加条件（`divergence.condition`）、
   哪个键路径（`divergence.key`）使入口改变；单条件可复现为 `single`，
   多条件共同作用为 `combined`，并附逐条件探针 `probes`。

## 用法

```ts
import { evaluateGate } from "conditional-diff-gate";
import { readFileSync } from "node:fs";

const result = evaluateGate({
  packageName: "victim-pkg",
  specifier: "victim-pkg",
  exports: {
    ".": {
      development: "./dev/main.js",          // 未经审查的前置入口
      node: { import: "./dist/main.js", default: "./dist/main.cjs" },
      default: "./dist/main.cjs",
    },
  },
  baselineConditions: ["import", "node", "default"],
  additionalConditions: ["development"],     // 用户构建带入
  allowedConditions: ["development"],        // 调用方允许集合
  manifest: ["dist/main.js", "dist/main.cjs", "dev/main.js", "package.json"],
  reviewed: { "dev/main.js": "<sha256-hex>" },
  readFile: (rel) => readFileSync(new URL(`node_modules/victim-pkg/${rel}`, import.meta.url)),
});

if (result.decision === "deny") {
  // result.reason:
  //   deny-blocked | deny-unresolvable | deny-missing-from-manifest
  //   deny-unreviewed-entry | deny-hash-mismatch | deny-hash-unreadable
  console.log(result.reason, result.divergence.condition, result.divergence.key);
}
```

也可单独使用解析器：

```ts
import { resolveExports, deriveSubpath } from "conditional-diff-gate/resolver";

const subpath = deriveSubpath("victim-pkg/feat", "victim-pkg"); // "./feat"
const resolved = resolveExports(exportsField, subpath, ["import", "node", "default"]);
// resolved.target / resolved.blocked / resolved.trace
```

## 输出要点

- `decision`：`allow` / `deny`；`reason`：具体裁决原因。
- `baseline` / `augmented`：两次解析结果，含完整 `trace`（尝试过的键、结果、键路径）。
- `divergence.changed`：入口是否改变；`condition`、`attribution`、`key`、`baselineKey`、`probes`。
- `manifest.selectedListed` / `manifest.baselineListed`：清单命中情况。
- `review`：已审表登记情况、期望/实际 SHA-256 及比对结论。
- `ignoredConditions`：不在允许集合内、被当作未传入的附加条件。

## 测试

```sh
npm test
```

覆盖：`development` 前置条件攻击、已审查入口放行、允许集合外条件忽略、
`null` 封锁、文件清单缺失，以及哈希不一致、无法读取、联合归因、
键序回溯、通配替换、数组回退与路径逃逸等边界。
