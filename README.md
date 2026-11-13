# 可信类型按汇分派

createHTML的返回值若被拿去赋给脚本地址，类型标记就失去意义。HTML、Script、ScriptURL三种结果不能互换；ScriptURL拒绝javascript和data，默认策略只在没有具名策略时参与。createHTML的结果写入innerHTML且不含事件属性时放行。文档管线调用可导入的TypeScript库做这次裁决。把ScriptURL的产物传给innerHTML接口，核对拒绝。

## 规则

- `TrustedHTML` / `TrustedScript` / `TrustedScriptURL` 是带独立标记（nominal brand）的三种结果，不能互换；汇（sink）按期望类型逐一核对，类型不符即抛 `TypeError`。
- `createScriptURL` 对最终产物做 scheme 校验，拒绝 `javascript:` 与 `data:`（大小写、前导空白均覆盖），即使策略回调想改写回来也会被拦截。
- 默认策略 `default` 仅在不存在任何具名策略时参与字符串升级；一旦注册具名策略，`defaultPolicy` 返回 `null`，字符串入汇直接拒绝。
- `innerHTML` 汇接收 `TrustedHTML`，并扫描事件处理属性（`on*`，大小写不敏感）；含事件属性即拒绝，无事件属性放行。

## 结构

- `lib/types.ts`：三种带标记结果及类型守卫。
- `lib/policy.ts`：`TrustedTypePolicyFactory`、具名/默认策略注册、ScriptURL scheme 裁决。
- `lib/sinks.ts`：按汇分派的 `TrustedTypeEnforcer`（含事件属性扫描与默认策略回退）。
- `lib/pipeline.ts`：文档管线 `DocumentPipeline`，所有写入都经 enforcer 裁决。
- `index.ts`：统一导出入口，另导出共享的 `trustedTypes` 工厂。

## 用法

```ts
import { createPipeline, TrustedTypePolicyFactory } from "./index.ts";

const { factory, pipeline } = createPipeline(new TrustedTypePolicyFactory());
const policy = factory.createPolicy("sanitizer");

// 放行：TrustedHTML 且无事件属性
pipeline.writeInnerHTML(policy.createHTML("<div><p>hello</p></div>"));

// 拒绝：事件处理属性
// pipeline.writeInnerHTML(policy.createHTML('<img src=x onerror="alert(1)">'));

// 拒绝：把 TrustedScriptURL 交给 innerHTML 汇
const url = policy.createScriptURL("https://example.com/a.js");
// pipeline.writeInnerHTML(url); // TypeError: Sink requires TrustedHTML, received TrustedScriptURL

// 拒绝：禁用 scheme
// policy.createScriptURL("javascript:alert(1)"); // TypeError
```

## 测试

```sh
npm test
```

基于 `node --experimental-strip-types --test`（Node 23），相对导入使用显式 `.ts` 扩展名。
