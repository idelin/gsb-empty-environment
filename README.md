# Unicode规范化路径绕过

静态资源网关按前缀 /private/ 拒绝访问，攻击者可用全角斜线、兼容字符或组合字符拼出另一种写法，等文件系统层归一化后又指向受保护目录。为 TypeScript 资源网关写可导入的路径判定包：先要求输入为合法 Unicode，再同时计算 NFC 与 NFKC 形式，任一形式命中受保护前缀都拒绝；两种形式的分隔符不一致或出现点段也拒绝。用全角点和斜线、分解字符、已规范路径及合法中文文件名验证。

## 使用

```ts
import {
  assertResourcePathAllowed,
  inspectResourcePath,
  isResourcePathAllowed,
} from "./index.ts";

isResourcePathAllowed("/public/图片/封面.png"); // true
isResourcePathAllowed("／private／secret.txt"); // false

const decision = inspectResourcePath("/public/．/index.html");
// decision.allowed === false
// decision.reason === "dot-segment"
// decision.nfkc === "/public/./index.html"

assertResourcePathAllowed("/public/..//private/secret.txt");
// throws ResourcePathRejectedError with reason "dot-segment"
```

包导出：

- `inspectResourcePath(path)`：返回 `{ allowed, reason, nfc, nfkc }`，便于网关记录拒绝原因。
- `isResourcePathAllowed(path)`：返回布尔值，也是默认导出。
- `assertResourcePathAllowed(path)`：允许时返回原路径，否则抛出 `ResourcePathRejectedError`。

## 判定顺序

1. 输入必须是非空字符串，包含合法 Unicode 标量序列，且不含 NUL。
2. 在做出任何路径判断前，同时计算 NFC 与 NFKC 形式。
3. NFC 或 NFKC 任一形式以 `/private/` 开头（或恰为 `/private`）时拒绝。
4. 两种形式中 `/` 连续段的顺序或长度不一致时拒绝，防止全角斜线等兼容字符只在一种形式中变成路径分隔符。
5. 两种形式中任一形式出现 `.` 或 `..` 路径段时拒绝，防止文件系统解析点段后进入受保护目录。

普通兼容字符会先经过 NFKC 再检查；例如合法中文文件名和公开目录中的全角字母文件名不会被保护前缀规则误判，但任何会改变分隔符结构的输入都会被拒绝。

## 测试

```sh
npm test
```
