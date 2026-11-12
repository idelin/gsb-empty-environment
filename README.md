# 结构化来源（Origin）库

安全消息宿主（postMessage 接收端）必须比较**来源标识本身**，而不是来源的
显示字符串。浏览器里沙箱文档的 `event.origin` 是字符串 `"null"`，而任何
不透明来源序列化后都是同一个 `"null"`——字符串比较无法区分它们，
`origin === "null"` 之类的判断会把不同沙箱混为一谈，造成跨域消息串域。

本库用 TypeScript 把来源建模为不可变的结构化对象：

- **普通来源（TupleOrigin）**：由 scheme（方案）、ASCII host（主机）和
  有效端口（1–65535 的整数）组成；scheme/host 规范化为小写，端口省略时
  采用 scheme 默认端口（http/ws=80、https/wss=443、ftp=21）。
- **不透明来源（OpaqueOrigin）**：每次构造生成独立随机身份
  （`symbol` 令牌 + UUID），外部无法根据显示字符串重建或伪造；
  两个独立生成的不透明来源**永远不同源**。
- **序列化仅用于显示**：`serializeOrigin()` 对不透明来源返回 `"null"`，
  只供日志/调试使用；`parseTupleOrigin("null")` 会直接抛错，杜绝把
  字符串 `"null"` 反序列化成某个不透明来源的反模式。
- **授权始终比较结构**：`sameOrigin()` 对普通来源逐字段比较，对不透明
  来源比较身份令牌；`SecureMessageHost` 只接受命中允许列表结构的消息。

不透明身份保存在模块私有的 `WeakMap` 中、不出现在对象字段里，因此：
显式 `cloneOrigin()`（模拟同一沙箱内的继承导航）会保留身份并保持同源；
而 `structuredClone()`（模拟 postMessage 跨代理/跨 realm 复制）得到的是
没有身份登记的新对象，天然成为另一个不透明来源，无法冒充原身份。

## 模块

- `src/origin.ts`：来源类型、构造、规范化、同源判定、序列化/解析、克隆
- `src/sandbox.ts`：沙箱文档来源模型，沙箱内 `about:blank` 继承同一
  不透明身份，独立沙箱身份各自不同
- `src/messaging.ts`：`SecureMessageHost` 安全消息宿主，结构化来源授权
- `index.ts`：桶导出，`import { tupleOrigin, sameOrigin } from "./index.ts"`

## 用法

```ts
import {
  tupleOrigin,
  opaqueOrigin,
  sameOrigin,
  serializeOrigin,
} from "./index.ts";
import { SecureMessageHost } from "./src/messaging.ts";

const app = tupleOrigin("https", "app.example.test");       // 端口默认 443
const evil = tupleOrigin("https", "app.example.test", 8443);
sameOrigin(app, evil);                                       // false：结构不同

const sandboxA = opaqueOrigin();
const sandboxB = opaqueOrigin();
serializeOrigin(sandboxA) === serializeOrigin(sandboxB);    // true：都是 "null"
sameOrigin(sandboxA, sandboxB);                              // false：身份不同

const host = new SecureMessageHost([app, sandboxA]);
host.receive({ data: "ok", senderOrigin: app });             // accepted
host.receive({ data: "x", senderOrigin: sandboxB });         // rejected
```

## 测试

```sh
node --experimental-strip-types --test
```

覆盖：默认端口等价与非默认端口隔离、scheme/host 大小写规范化、非法端口与
非 ASCII 主机拒绝、沙箱文档的不透明身份继承、独立沙箱互不同源，以及不同
不透明来源之间消息发送不串域（含字符串 `"null"` 反模式对照）。
