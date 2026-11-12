# Trusted Types策略合成

攻击者可控字符串经多个Trusted Types策略转手后可能被当成可信HTML。为TypeScript前端实现可导入的策略合成库：策略带名称和输出品牌，sink只收许可来源策略创建的值；TrustedHTML字符串化后不得重铸品牌，default策略不能扩大许可。用字符串拼接、重复策略名和default回退测试，非许可值不得到达模拟innerHTML。

## 用法

```ts
import { PolicyRegistry, TrustedHtmlSink } from './index.ts';

const registry = new PolicyRegistry();
const sanitizer = registry.createPolicy('sanitizer', (input) => sanitize(input));

const el = { innerHTML: '' };
const sink = new TrustedHtmlSink(el, [sanitizer]);
sink.setHTML(sanitizer.createHTML(userInput)); // 仅许可策略的值可写入
```

- `TrustedHTML` 只能由 `Policy.createHTML` 创建；`toString()`/拼接得到普通 `string`，无法重铸品牌。
- `registry.getPolicy(name)` 未命中时回退到 default 策略（自动 HTML 转义），其输出品牌仍为 `default`，不会获得其他策略的许可。
- `registry.createComposedPolicy(name, { from, createHTML })` 可合成声明了上游策略的值，输出带全新品牌。

测试：`npm test`
