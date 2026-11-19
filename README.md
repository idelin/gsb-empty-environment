# V8序列化载荷标签白名单

服务端用 v8.deserialize 还原不可信客户端发来的二进制消息，攻击者可构造宿主对象标签、超深嵌套或指向未出现对象的引用，让反序列化先耗尽资源或产出非预期对象。为 TypeScript 服务写供消息入口在反序列化前调用的载荷预检包，只扫字节、不还原对象：头部须为 0xFF 加版本 15；本题只放行这些标签：_ undefined、0 null、T、F、I int32（zigzag 变长整数）、N double（8 字节）、" 单字节串（变长长度）、o 对象（键值对后接 { 与属性数）、A 稠密数组（长度后接 $ 与属性数和长度）、^ 对象引用（编号）；宿主对象 \、各类缓冲区、Map、Set、RegExp、BigInt 及未知标签一律拒绝；嵌套不超过 32 层，属性与元素总数不超过 4096，引用编号须指向已出现的对象，字符串长度须与剩余字节吻合。用宿主对象标签、33 层嵌套、前向引用、截断字符串和合法嵌套对象验证。

## 用法

消息入口在 `v8.deserialize` 之前调用预检，只扫字节、不还原对象：

```ts
import v8 from "node:v8";
import { validateV8Payload } from "./index.ts";

function onMessage(data: Uint8Array) {
  const result = validateV8Payload(data);
  if (!result.ok) {
    // result.code / result.offset 可用于日志与限流
    throw new Error(`rejected payload: ${result.code} at ${result.offset}`);
  }
  return v8.deserialize(data);
}
```

- `validateV8Payload(data, options?)` 返回 `{ ok: true }` 或 `{ ok: false, code, message, offset }`，不抛异常。
- `assertV8Payload(data, options?)` 不合法时抛出 `PreflightError`。
- `options` 可覆盖 `maxDepth`（默认 32）与 `maxProperties`（默认 4096）。

## 测试

```sh
npm test
```
