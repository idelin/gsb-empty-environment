# 凭证无关跨源

COEP为credentialless时，没有CORP的跨源no-cors响应仍带着对方身份信息。这种情况把响应改成不透明且去掉身份；要求require-corp时则直接失败。带CORP same-origin且同源的响应保持原样。资源加载器调用可导入的TypeScript库做这次裁决。切换COEP的两个取值并去掉CORP。

## 用法

```ts
import { adjudicateResponse } from "./index.ts";
import { COEPResourceLoader } from "./resource-loader.ts";

const decision = adjudicateResponse({
  coep: "credentialless",
  corp: null,
  crossOrigin: true,
  mode: "no-cors",
  response,
});

// decision.action: "allow" | "opaque" | "block"
// opaque 响应无 body、无 headers、无 URL，不会泄露来源身份。

const loader = new COEPResourceLoader({
  coep: "require-corp",
  origin: "https://app.example",
});

// 无 CORP 的跨源 no-cors 响应会抛出 COEPPolicyError。
const resource = await loader.load("https://cdn.example/data");
```
