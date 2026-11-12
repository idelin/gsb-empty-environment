# 混合内容升级失败处置

安全页面自动将子资源升级到安全协议，失败时不能悄悄回落明文。写供TypeScript浏览器宿主调用的请求规划包，资源类型和文档策略共同决定升级或阻断，目标端口按明确映射规则处理；所有重定向沿升级要求继续检查。用模拟加载器验证失败重试、内网地址及缓存命中不会改变原安全要求。

## 用法

```ts
import { loadWithPlan, planRequest } from "./src/index.ts";

const result = await loadWithPlan({
  url: "http://cdn.example.com/app.js",
  resourceType: "script",
  policy: {
    secureContext: true,
    upgradeInsecureRequests: true,
    blockAllMixedContent: false,
  },
  loader: (url) => fetch(url), // 浏览器宿主注入实际加载能力
});
```

## 规则

- 决策矩阵：非安全文档直接放行；`upgradeInsecureRequests` 全部升级；`blockAllMixedContent` 全部阻断；默认情况下 image/audio/video 自动升级，其余可阻断类型阻断。
- 端口映射：默认仅 `80→443`，可通过 `portMap` 显式扩展；未映射的显式端口原样保留。
- 重定向：每个跳点重新 `planRequest`，升级要求沿重定向链持续生效。
- 失败处置：升级后的请求失败即返回失败，绝不回落明文；阻断时不发起网络请求。
- 内网地址与缓存命中不享受任何豁免，安全要求保持一致。

## 测试

```sh
npm test   # node --experimental-strip-types --test
```
