# 清单审查器：导出面分析组件

面向 Android `AndroidManifest.xml` 的导出面（export surface）分析器，零依赖 TypeScript 实现。

## 能力

- **隐式导出识别**：按 `targetSdkVersion` 应用平台规则——组件带 `intent-filter` 且未声明
  `android:exported` 时，targetSdk < 31 视为隐式导出，targetSdk >= 31 视为非法/不导出并产生
  `missing-exported-attribute` 发现；`activity-alias` 走同样的判定路径。
- **权限保护核对**：解析组件级与 `<application>` 级兜底权限，识别自定义权限的
  `protectionLevel`（含缺失时默认 `normal`）、平台权限等级，输出未保护、弱权限（normal/dangerous/
  未知等级，第三方可申请）、签名级保护等结论；`provider` 单独核对 read/write 权限与
  `grantUriPermissions` 绕过面。
- **重定向链路追踪**：把清单中的 `activity-alias -> targetActivity` 与调用方提供的运行时
  重定向边（`startActivity`、`bindService`、`sendBroadcast`、`content-uri-grant` 等）组成有向图，
  从每个导出入口做 BFS，追踪到达非导出组件的完整路径，记录每跳类型、Intent 是否透传、
  所需权限与签名级阻断点；自动防环并限制最大跳数。
- **可利用链路输出**：`redirectChains` 给出每条链路及 `exploitable`/`severity`，
  `redirectOnlyComponents` 汇总“仅靠重定向可达”的私有组件（自身 `exported=false`，
  但存在从导出入口出发的路径），同时在 `findings` 中生成 `redirect-to-private-component` 发现。

## 模块结构

- `src/types.ts` — 领域模型（清单、组件、权限、重定向边、链路、报告）
- `src/parser.ts` — 零依赖清单 XML 解析器（支持命名空间属性、简写类名、多 authority）
- `src/exportSurface.ts` — 隐式导出判定与权限保护核对
- `src/redirectAnalysis.ts` — 重定向图构建、BFS 链路追踪与重定向专属组件汇总
- `src/analyzer.ts` — 门面：解析 → 导出面 → 重定向 → 汇总 `AnalysisReport`
- `test/` — `node:test` 测试与 `test/fixtures/sample-manifest.xml` 夹具

## 用法

```ts
import { analyzeManifest } from './src/index.ts';
import type { RedirectEdge } from './src/index.ts';

const redirects: RedirectEdge[] = [
  { source: 'com.example.Router', target: 'com.example.internal.Secret',
    kind: 'startActivity', intentPassthrough: true },
];

const report = analyzeManifest(manifestXml, { redirects, maxHops: 8 });
console.log(report.redirectOnlyComponents);
console.log(report.redirectChains.filter((chain) => chain.exploitable));
```

重定向边来自静态分析无法独立确定的运行时行为（组件内 `startActivity`/`bindService` 等调用），
由上游数据流分析提供；`activity-alias` 边由清单自动推导，无需传入。

## 测试

```sh
npm test
```

`test/redirects.test.ts` 使用一组 `android:exported="false"` 的组件（内部 Activity、Service、
Receiver、Provider 以及重定向环）验证：它们不直接出现在导出面上，却能通过
`RouterActivity` 等导出入口经一跳或多跳重定向到达，并正确区分可利用链路与被签名权限阻断的链路。
