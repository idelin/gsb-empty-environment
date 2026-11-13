# 源代理簇锁域

document.domain被改短后，不同端口的页面会变成同源。响应带头要求源代理簇时，document.domain的赋值必须失败，源比较保持主机加端口。没有该头时不改变现有源比较。文档管线调用可导入的TypeScript库做这次裁决。带头和不带头各赋值一次父域。

## 裁决规则

- 无 `Origin-Agent-Cluster` 头：保留既有行为，`document.domain` 可缩短为父域；
  两页（不同端口）缩短到同一父域后按宽松规则视为同源。
- 带 `Origin-Agent-Cluster: ?1`：文档进入源键控（origin-keyed）代理簇，
  `document.domain` 赋值抛出 `SecurityError`，源比较始终按
  `协议 + 主机 + 端口` 严格进行，端口不再被忽略。

## 库结构

可导入的 TypeScript 库，入口为 `index.ts`：

- `src/origin.ts`：元组源（scheme/host/port）、URL 解析与严格 `isSameOrigin`。
- `src/agentCluster.ts`：解析 `Origin-Agent-Cluster` 头，仅 `?1` 判定为源键控。
- `src/document.ts`：`DocumentModel` 与 `document.domain` 赋值器，
  含源键控守卫、父域校验，以及严格/宽松两条同源比较路径。
- `src/pipeline.ts`：文档管线，`runDomainLockTrial()` 对同一组不同端口页面
  分别在「无头」与「带头」两种场景下各赋值一次父域并给出裁决。

## 用法

```ts
import { runDomainLockTrial } from "./index.ts";

const report = runDomainLockTrial();
for (const scenario of report.scenarios) {
  console.log(scenario.label, {
    originKeyed: scenario.originKeyed,
    assignmentA: scenario.assignmentA,
    assignmentB: scenario.assignmentB,
    strictSameOrigin: scenario.strictSameOrigin,
    effectiveSameOrigin: scenario.effectiveSameOrigin,
  });
}
```

默认场景：`https://a.example.com:8080` 与 `https://b.example.com:9090`，
父域 `example.com`。可用 `runDomainLockTrial({ scheme, portA, portB, ... })`
自定义。

## 测试

```sh
npm test
```

使用 Node 内置测试运行器与原生 TypeScript 类型擦除（Node 23，
`--experimental-strip-types`），无需额外依赖。
