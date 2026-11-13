# import map 最长前缀与 integrity

`index.ts` 是可被模块加载器导入的 TypeScript 库，负责 import map 裁决：

- `scopes` 按最长前缀匹配，与声明顺序无关；短前缀不能覆盖长前缀。
- 选中的 scope 没有对应键时，才回退到顶层 `imports`。
- 无 `scopes` 时，`imports` 的精确匹配正常工作；以 `/` 结尾的键支持前缀映射。
- `null` 映射会阻止说明符继续解析。
- integrity 支持 `sha256`、`sha384`、`sha512`。元数据缺失、算法未知、格式错误或摘要不符都会抛 `IntegrityError`。
- `loadModule` 对解析后的地址只发起一次请求；integrity 失败是终止状态，不会改用无 integrity 的地址重试。
- 对象形式的映射默认要求 integrity；普通字符串映射可通过 `requireIntegrity: true` 强制要求。

## API

```ts
import {
  resolveImportMap,
  resolveImportMapDetailed,
  verifyIntegrity,
  loadModule,
} from "./index.ts";

const url = resolveImportMap(importMap, "pkg", {
  baseUrl: "https://example.com/",
  moduleUrl: "https://example.com/lib/deep/app.js",
});

const loaded = await loadModule(importMap, "pkg", {
  baseUrl: "https://example.com/",
  fetch: (url) => fetch(url),
});
```

`resolveImportMapDetailed` 返回命中的 scope、映射键、匹配类型和 integrity 元数据；`loadModule` 返回已验证的 `content`、`text`、`source` 与解析结果。

## 测试

`npm test` 覆盖长/短 scope 前缀（两种声明顺序）、无 scopes 的精确匹配、正确摘要、错误摘要、缺失元数据、未知算法，以及失败后只请求一次、不回退到无 integrity 地址。
