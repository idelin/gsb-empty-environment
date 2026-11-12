# Import Map 注入准入

面向 TypeScript 页面渲染服务的 import map 准入包。模块加载前对 import map
进行一次性注册与校验，所有解析结果都必须落在许可来源（allow list）内。

## 安全保证

- **只许一份 map**：`register()` 成功后再次注册抛出 `SECOND_MAP`；首次
  `resolve()` 会锁定守卫，杜绝渲染过程中被用户内容注入第二份 map。
- **来源准入**：`imports` 与每个 `scopes` 条目的目标 URL 必须是 http/https
  绝对地址，且其 origin 出现在 `allowedOrigins` 中，否则抛出
  `DISALLOWED_ORIGIN`。带凭据（userinfo）的 URL 一律拒绝。
- **scope 最长前缀匹配**：scope 键规范化为目录前缀（无尾斜杠自动补 `/`），
  解析时按前缀长度降序选择，确保 `/a/b/` 优先于 `/a/`，且不会把
  `/plugins-evil/` 误匹配到 `/plugins/`。
- **尾斜杠规范化不越界**：前缀映射的 key 与 target 必须同时以 `/` 结尾；
  拼接后的 URL 重新解析，若经 `..` 等点段逃逸出 target 目录则抛出
  `PREFIX_ESCAPE`。规范化后发生碰撞的两个 scope 直接拒绝。
- **重复键拒绝**：JSON 文本先经独立词法扫描（`scanJsonKeys`），同一对象内
  重复键（含 Unicode 转义等价形式）抛出 `DUPLICATE_KEY`，避免依赖
  `JSON.parse` 的“后者覆盖”行为。
- **`__proto__` 拒绝**：JSON 文本与对象输入在任意层级出现 `__proto__`
  键均抛出 `PROTO_KEY`，防止原型污染。

## 用法

```ts
import { ImportMapError, ImportMapGuard } from './index.ts';

const guard = new ImportMapGuard({
  allowedOrigins: ['https://cdn.example.com', 'https://assets.example.com'],
});

// 服务启动、模块加载前注册唯一一份 import map（接受字符串或已解析对象）
guard.register(importMapText);

// 渲染页面解析模块说明符；首次调用即锁定注册
const href = guard.resolve('react', 'https://cdn.example.com/pages/home');
```

解析顺序：命中最长 scope 前缀 → 该 scope 内精确/前缀匹配 → 顶层 `imports`
→ 许可来源内的绝对 URL 直通；其余情况 fail-closed 抛出 `ImportMapError`。

## 错误码

| code | 含义 |
| --- | --- |
| `INVALID_JSON` | 不是合法 JSON 或词法扫描失败 |
| `DUPLICATE_KEY` | 同一对象出现重复键 |
| `PROTO_KEY` | 出现 `__proto__` 键 |
| `INVALID_SHAPE` | 结构不符（未知顶层键、非字符串目标、scope 碰撞等） |
| `INVALID_URL` | 不是 http/https 绝对 URL、含凭据或许可来源格式错误 |
| `DISALLOWED_ORIGIN` | origin 不在许可来源内 |
| `PREFIX_MISMATCH` | 前缀 key 映射到了不带尾斜杠的 target |
| `PREFIX_ESCAPE` | 解析结果逃逸出 target 目录或 scope 边界 |
| `SECOND_MAP` | 试图注册第二份 import map |
| `GUARD_LOCKED` | 锁定后尝试注册 |
| `NO_MATCH` | 无匹配且不是许可来源内的绝对 URL |

## 测试

```sh
npm test
```

测试基于 Node 内置 test runner（`node --experimental-strip-types --test`），
覆盖 scope 覆盖与最长前缀、尾斜杠逃逸、第二份 map、重复键、`__proto__`
及来源准入等场景，见 `src/import-map-guard.test.ts`。
