# CardDAV 地址簿属性可见性与查询泄漏防护

共享地址簿对只读成员隐藏 `NOTE` 与 `X-PRIVATE-` 前缀属性。本包供 REPORT
（`addressbook-query`）处理器调用，防止通过 `prop-filter` / `text-match` 猜测隐藏属性，
或借助 `limit`、匹配条数、`getetag` 进行旁路推断。多取（multiget）与同步令牌不在范围内。

## 文件结构

- `src/vcard.ts`：最小 vCard 行模型（折行展开、组前缀/参数剥离、文本反转义、序列化）
- `src/visibility.ts`：可见性策略、过滤求值、address-data 裁剪、nresults 截断、etag
- `index.ts`：公共 API
- `test/visibility.test.ts`：23 个用例，`npm test` 运行（node 内置 test runner）

## 安全语义

1. **隐藏判定按属性名大小写不敏感**：`NOTE` 及任何 `X-PRIVATE-` 开头的属性对
   `canSeeHidden: false` 的调用者不可见，带组前缀的 `item1.NOTE` 同样隐藏。
2. **过滤只作用于可见属性**：求值前先按调用者视图裁剪 vCard，再跑 RFC 6352 的
   `prop-filter`。隐藏属性对只读成员视为“不存在”：
   - `is-not-defined` 命中（属性确实不在其视图里）；
   - 无 text-match 的存在性过滤不命中；
   - `text-match` 不命中；**否定条件（`negate-condition`）同样不命中**，
     否则攻击者可用“值不等于猜测串”反推隐藏内容；
   - `allof` / `anyof` 对裁剪后的属性集正常求值。
3. **address-data 请求隐藏属性按未请求处理**：不报错、不输出；只请求隐藏属性时返回
   仅含 `BEGIN/VERSION/END` 的结构卡片。`VERSION` 作为结构字段始终保留。
4. **nresults 只统计可见匹配**：先按可见视图过滤、再计数与截断；超出限制时
   `truncated: true` 且 `limitStatus = 507`（Insufficient Storage，RFC 6352 §10.3），
   `totalVisibleMatches` 给出真实可见匹配数。隐藏属性的内容变化不改变计数与页内容。
5. **getetag 基于调用者可见视图**：对裁剪并规范化（CRLF）后的 vCard 取 SHA-256。
   只读成员的 etag 不随 `NOTE` / `X-PRIVATE-*` 内容变化；可见属性变化才会变；
   所有者（`canSeeHidden: true`）看到完整内容的 etag。

## REPORT 处理器集成示例

```ts
import { runAddressBookQuery } from "./index.ts";

const policy = { canSeeHidden: caller.role === "owner" };
const result = runAddressBookQuery(
  storedContacts.map((c) => ({ href: c.href, vcard: c.rawVCard })),
  {
    filter: parsedAddressbookQueryFilter,   // AddressBookFilter | null
    nresults: parsedNResults,               // number | null
    addressData: parsedAddressDataRequest,  // AddressDataRequest | null
  },
  policy,
);

// result.contacts[].href / visibleVCard / etag 直接组装响应；
// result.truncated 为 true 时响应状态码取 result.limitStatus（507）。
```

也可按需单独使用 `cardMatchesFilter`、`buildVisibleVCard`、`getEtag`、
`applyNResults`（处理器已自行收集匹配项时的纯截断辅助）。

## 测试

```sh
npm test
```

覆盖：针对 `NOTE` 的 text-match 猜测（含否定反推、is-not-defined、allof）、
`X-PRIVATE-*` 过滤、limit 计数与 507 截断、隐藏属性改动前后 etag 不变、
address-data 隐藏属性按未请求处理，以及所有者视图对照。
