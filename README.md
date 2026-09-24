# diagnostic-relocation

诊断记录绑定旧版本范围与上下文摘要，在连续多版本编辑后尝试重定位，返回三种结果：

- **definite（确定）**：通过可组合位置映射完成有证据的身份跟随（`via: 'identity'`），
  或在身份路径不可用（历史回收 / 废弃分支）时由唯一上下文匹配得出（`via: 'context'`）。
- **ambiguous（模糊）**：相同上下文出现多次，返回全部候选与歧义证据，绝不任意选第一个。
- **invalid（失效）**：目标被全部删除，或上下文中无任何匹配。被删除后在别处出现的相同
  文本只会作为“已排除候选”附在证据里，不能自动复活诊断；只有带稳定 ID 的移动操作
  才能被精确跟随。

## 设计

- `src/mapping.ts`：段式位置映射（kept / deleted 段），支持 `composeMappings`（多版本编辑
  组合成单一映射）、`invertMapping`（撤销分支）、`mapOffset`（左右亲和性）与 `mapRange`
  （intact / shrank / deleted）。
- `src/history.ts`：版本图，支持撤销分支、三方路径映射（LCA + 逆向边）、分支合并
  （`merge`，把另一分支的编辑平移重放到当前 tip）、历史回收（`reclaim`：把被丢弃的编辑
  压缩成一条组合映射检查点；pinned 版本保留身份路径）。
- `src/diagnostic.ts`：诊断记录（UTF-16 范围 + 前后文摘要），`relocate` 产出候选生成与
  排除证据（`evidence.steps`）。
- `src/utf16.ts`：所有公开偏移为 UTF-16 code unit；任何切开代理对的编辑或诊断范围
  直接抛出 `SurrogatePairError`。LF 与 CRLF 均按 code unit 计数（CRLF = 2）。

## 运行

```sh
npm test     # node --test test/*.test.ts（Node.js 内置测试运行器，27 项）
npm run demo # 运行 examples/demo.ts 中的 9 个场景样例
```

要求 Node.js >= 22.6（依赖内置 TypeScript 类型擦除；开发版本为 v23.9.0）。

## 对照参考

`test/mapping.test.ts` 中的 `referenceMapRange`（`test/helpers.ts`）是一个独立的
token 数组逐步映射参考实现。组合映射与逐步折叠映射在重复文本、整段搬移、分支合并
场景下逐范围对照一致。
