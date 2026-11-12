# CORP 同站判定阻断

攻击者页面想读取受CORP保护的跨源图片，并借同注册域下的不受信子域绕过same-site判断。为TypeScript网络栈实现可导入的CORP判定包：same-site按公共后缀表的可注册域比较且scheme一致，same-origin精确比较，非法头值视为无策略。用github.io式私有后缀、http到https和异端口测试，被阻断响应体不得交付。

## 用法

```ts
import {
  evaluateCorp,
  applyCorpPolicy,
  readCorpResponseBody,
} from "netnew-20261112-0097";

// 攻击者在 evil.github.io，受保护图片在 victim.github.io。
// github.io 是 PSL 私有后缀，两者不是同站，因此被阻断。
const decision = evaluateCorp({
  initiatorOrigin: "https://evil.github.io",
  resourceUrl: "https://victim.github.io/private.png",
  headers: {
    "Cross-Origin-Resource-Policy": "same-site",
  },
});

decision.allowed; // false
decision.blocked; // true
decision.reason;  // "site-mismatch"
```

判定规则：

- `same-origin`：scheme、host、有效端口完全一致。
- `same-site`：scheme 一致，且公共后缀表（含私有后缀）计算出的可注册域一致；端口不参与站点比较。IP 或无可用可注册域的主机退化为精确 origin 比较。
- `cross-origin`：总是允许。
- 头缺失、空值、未知值、逗号合并的重复值等非法值均视为“无策略”，不阻断。

## 不交付被阻断的响应体

`applyCorpPolicy` 只在允许时附带 `body`；被阻断的结果完全没有 `body` 属性：

```ts
const result = applyCorpPolicy({
  initiatorOrigin: "https://evil.github.io",
  resourceUrl: "https://victim.github.io/private.png",
  corpHeader: "same-site",
  body: protectedBytes,
});

result.body; // undefined，且 "body" in result === false
```

对于 Fetch 响应，使用 `readCorpResponseBody`、`guardCorpResponse` 或
`filterCorpResponse`。被阻断时会先取消响应流，再返回 `null`，不会读取或返回任何字节。

## 公共后缀表

默认规则包含常见多级 ICANN 后缀（如 `co.uk`）和私有后缀（如 `github.io`、
`pages.dev`、`blogspot.com`），并实现了 `*` 通配规则与 `!` 例外规则。
需要完整或自定义 PSL 快照时，可使用 `createPublicSuffixMatcher(pslText)`。

```ts
import { createPublicSuffixMatcher } from "netnew-20261112-0097/public-suffix";

const matcher = createPublicSuffixMatcher("github.io\n*.ck\n!www.ck\n");
matcher.getRegistrableDomain("alice.github.io"); // "alice.github.io"
matcher.getRegistrableDomain("bob.github.io");   // "bob.github.io"
```

## 测试

```sh
npm test
```

测试覆盖 github.io 私有后缀、`http` 到 `https` 的 scheme 变化、异端口、
非法头值，以及被阻断响应体不进入结果或 Fetch 读取路径。
