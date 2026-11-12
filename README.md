# Node包exports子路径模式越界

Node 的包入口由 package.json 的 exports 映射决定，插件加载器把用户给出的名称拼进说明符，攻击者可借带 .. 的子路径、百分号编码的分隔符或含 node_modules 的目标跳出包目录。为 TypeScript 插件加载器写供模块说明符解析调用的 exports 解析包：输入包名、exports 映射（含条件对象与带单个 * 的子路径模式，条件键按声明顺序匹配 import、node、default，目标 null 表示屏蔽）和请求子路径；键含 * 时取前缀最长的模式，* 的替换内容含 .. 段、百分号编码的 /、\ 或 . 段、以及 node_modules 段一律拒绝；目标须以 ./ 开头，解析后的路径不得离开包目录；子路径没有对应键则拒绝，不得回退到文件系统直接查找；* 替换内容为空拒绝。用 ..、%2e%2e、%2F、node_modules 段、最长前缀竞争和 null 屏蔽验证。

## API

```ts
import { resolveExports } from "./index.ts";

resolveExports("pkg", {
  ".": { import: "./dist/index.js", default: "./dist/index.cjs" },
  "./feature/*": "./dist/feature/*.js",
}, "./feature/one");
// "./dist/feature/one.js"
```

`resolveExports(packageName, exportsMap, subpath, options?)` returns a
package-relative target beginning with `./`. It is a pure resolver: it never
checks or falls back to the file system. Rejections throw
`ExportsResolutionError` with a Node-style `code`.

Additional exports:

- `tryResolveExports(...)` returns `null` on a deliberate rejection.
- `resolveModuleSpecifier(...)` returns `pkg/dist/feature/one.js` style output.
- `resolveExportsResult(...)` returns
  `{ packageName, subpath, target, specifier }`.
- `resolvePackageExports`, `resolveExport`, and `resolvePackageSubpath` are
  aliases; `resolveExports` is the default export.

## Security rules

- Only active condition keys are considered, in object declaration order;
  `default` is always active. A selected `null` target blocks resolution.
- Wildcard keys contain exactly one `*`; the pattern with the longest matching
  prefix wins. Exact keys win before patterns.
- Wildcard replacements cannot be empty or contain `.`/`..` segments,
  `node_modules` segments, backslashes, NUL bytes, encoded separators, or
  recursively encoded traversal segments.
- Targets must start with `./`; normalized targets cannot escape the package
  root or enter `node_modules`.
- A missing subpath is not exported. There is no direct file-system lookup.

## Test

```sh
npm test
```
