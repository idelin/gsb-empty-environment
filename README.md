# HSTS 策略解析与子域匹配

攻击者经明文 HTTP 注入 Strict-Transport-Security 头，或在子域发 max-age=0 想解除父域保护。为TypeScript浏览器网络栈写可导入的 HSTS 存储：只处理 HTTPS 响应里的头，IP 字面量主机忽略；max-age 须为十进制整数，指令名不分大小写，重复指令使整头无效，max-age=0 删除该条目；includeSubDomains 覆盖子域，子域的删除只影响自身；按标签边界匹配。测明文注入、重复指令、仿冒后缀。

## 使用

```ts
import { HstsStore } from "./index.ts";

const hsts = new HstsStore();

// 浏览器网络栈收到响应后调用。只有 HTTPS 响应会被处理。
hsts.processResponse(response.url, response.headers);

// 发起请求前判断：为 true 时，明文 HTTP 应升级到 HTTPS。
hsts.isSecure(requestUrl);
```

`processResponse()` 接受 `URL`/绝对 URL 字符串，以及 `Headers`、`Map`、
`[name, value]` 数组或普通对象形式的响应头。返回 `true` 表示该响应含有一个
完整、合法的 HSTS 字段；明文响应、IP 字面量主机和非法字段均返回 `false`。

可用 `new HstsStore({ now: () => timestamp })` 注入时钟，便于测试
`max-age` 到期。存储为进程内内存存储；`delete(host)` 只删除该主机的精确
条目，不会删除其父域策略。

## 规则

- 仅处理 `https:` 响应；明文响应不能新增或删除策略。
- IPv4、IPv6 字面量主机被忽略。
- `max-age` 必须是无符号十进制整数；`0` 删除当前主机的精确条目。
- 指令名和响应头名均大小写不敏感。
- 任一重复指令使整个头无效，已有的合法策略保持不变。
- `includeSubDomains` 按 DNS 标签边界覆盖子域，不使用字符串后缀匹配。
- 查询从主机自身开始逐级检查父域；子域的 `max-age=0` 只删除自身条目，
  父域策略仍可继续覆盖它。
