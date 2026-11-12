# Payment Handler来源绑定

攻击者想为他人的支付方式URL注册自己的Payment Handler。为TypeScript浏览器宿主实现可导入的注册校验包：支付方式清单须由方式URL所属源直接获取，不跟随跨源重定向；handler来源须被default_applications或supported_origins列出；清单撤销后handler不再响应。用跨源重定向、缺项和撤销测试。

## 用法

```ts
import { PaymentHandlerRegistry } from "./index.ts";

const registry = new PaymentHandlerRegistry(); // 默认使用全局 fetch（redirect: "manual"）

// 注册：从方式 URL 所属源直接获取清单（仅跟随同源重定向），
// 并校验 handler 来源被 default_applications 或 supported_origins 授权。
await registry.register("https://pay.example.com/pay/method.json", "https://wallet.example.com");

// 调度前查询。
registry.canHandle("https://pay.example.com/pay/method.json", "https://wallet.example.com");

// 周期性刷新：清单被撤销或不再授权时，handler 自动失效。
await registry.refresh("https://pay.example.com/pay/method.json");
```

## 安全不变量

- 清单获取不跟随跨源重定向（`CrossOriginRedirectError`），防止方式 URL 被劫持到攻击者源的清单。
- handler 来源必须出现在清单的 `default_applications`（按来源匹配）或 `supported_origins`（支持 `"*"`）中，否则抛出 `HandlerNotAuthorizedError`。
- `refresh()` 时清单获取失败（404/410/网络错误）视为撤销，该方式 URL 下全部 handler 不再响应；清单变更后未获授权的 handler 同样被移除。

## 测试

```sh
npm test
```
