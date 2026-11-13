# srcdoc沙箱不透明源

带allow-scripts又带allow-same-origin的srcdoc会继承父源并执行脚本。同时出现这两个允许标记则拒绝该组合；只有脚本允许时源必须是不透明的。两个标记都不出现时按不透明源且不执行。文档管线调用可导入的TypeScript库做这次裁决。四种标记组合都建一次srcdoc。

## 裁决矩阵

| `allow-scripts` | `allow-same-origin` | 裁决 | 源 | 脚本 |
| --- | --- | --- | --- | --- |
| 否 | 否 | 允许 | `opaque` | 不执行 |
| 是 | 否 | 允许 | `opaque` | 执行 |
| 否 | 是 | 允许 | `inherited` | 不执行 |
| 是 | 是 | 拒绝 | — | — |

## 用法

`src/sandbox-policy.ts` 是独立的裁决库，`src/srcdoc-pipeline.ts` 负责按裁决结果构造srcdoc。

```ts
import {
  adjudicateSandboxFlags,
  buildSrcdocMatrix,
} from "./index.ts";

const verdict = adjudicateSandboxFlags("allow-scripts");
// { allowed: true, origin: "opaque", executesScripts: true, ... }

const attempts = buildSrcdocMatrix();
// 四种标记组合各尝试一次；三种生成文档，冲突组合被拒绝
```

`buildSrcdocDocument(flags)` 遇到冲突组合会抛出 `SandboxPolicyError`，不会生成srcdoc。`renderSrcdocIframe(document)` 始终输出 `sandbox` 属性，包括空值 `sandbox=""`。

安全约束：

- `allow-scripts` 与 `allow-same-origin` 同时出现时，在构造srcdoc前拒绝。
- 允许脚本的裁决必须是不透明源；管线在构造文档时会再次校验该不变量。
- 未授予 `allow-scripts` 时不执行脚本；两个标记都未授予时保持不透明源。

运行验证：

```sh
npm test
npm run demo
```
