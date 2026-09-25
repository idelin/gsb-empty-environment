# json-path-move

按模式匹配路径，对 JSON 兼容树执行**重命名 / 移动**。支持通配符与捕获，先建立计划、再检查目标冲突，最后一次性应用；纯 TypeScript，零依赖，使用 Node.js（>= 23）原生类型擦除直接运行，测试基于内置 `node:test`。

## 路径语法

- 路径由 `/` 分段，如 `a/b/0`；前导 `/` 可选。
- 键中含 `/` 或 `\` 时用反斜杠转义：`weird\/key`、`a\\b`。
- 段作用于数组时须为非负整数下标；作用于对象时按键名查找（包括 `"0"` 这类数字形键名）。
- 对象键无序、数组有序：相等性比较对对象按键集合比较（与键序无关），对数组按下标顺序比较。

## 模式与替换

- `*`：匹配恰好一段并捕获；`**`：匹配零段或多段并捕获（贪婪、最长优先）。
- 替换模板中 `$1`、`$2`… 按出现顺序引用捕获；`**` 的捕获会展开为多段。
- 简写：模式中只有一个 `*`（或 `**`）捕获时，替换里可直接写 `*`（或 `**`）。
- 匹配采用**外层优先**：某节点命中后其子树整体被搬走，不再深入，也不会对新生成的节点重新扫描，因此源/目标互为祖孙（如 `a` → `a/b`）不会无限循环。

## 执行流程（两阶段）

1. **计划**：对原始树快照做匹配，得到 `{from, to}` 列表（`planMoves` / `--plan`）。
2. **冲突检查**（全部报错都携带具体路径 `err.path`）：
   - 目标已存在且值不同 → 冲突；值深度相等 → 幂等跳过（`noop`）。
   - 多个源映射到同一目标但值不同 → 冲突；值相同 → 合并。
   - 目标路径穿过标量、目标互为祖孙（非幂等）→ 冲突。
   - 目标位于将被移走的源子树内（含 `a` → `a/b`）→ 合法，先快照再重建。
3. **应用**：先按“深→浅、数组下标大→小”删除所有源，再按相反顺序写入所有目标。输入树不被修改（返回新树）。

## 使用

```bash
node src/cli.ts [--plan] <pattern> <replacement> [file]   # 无 file 时读 stdin
```

```ts
import { applyMoves, planMoves, JsonMoveError } from "./src/jsonmove.ts";

const out = applyMoves({ a: { x: 1 } }, "a/*", "b/$1"); // { a: {}, b: { x: 1 } }
```

## 运行样例

`sample/tree.json`：

```json
{ "a": { "x": 1, "y": 2 }, "b": { "y": 2 }, "weird/key": { "sub": true }, "list": ["a", "b", "c"] }
```

先看计划（注意 `/b/y` 已有相同值，标记为幂等）：

```console
$ node src/cli.ts --plan 'a/*' 'b/$1' sample/tree.json
[
  { "from": "/a/x", "to": "/b/x", "noop": false },
  { "from": "/a/y", "to": "/b/y", "noop": true }
]
```

应用：

```console
$ node src/cli.ts 'a/*' 'b/$1' sample/tree.json
{
  "a": {},
  "b": { "y": 2, "x": 1 },
  "weird/key": { "sub": true },
  "list": [ "a", "b", "c" ]
}
```

目标已有不同值 → 冲突，错误定位到具体路径，退出码 1：

```console
$ node src/cli.ts 'a/*' 'b/$1' sample/conflict.json
error: conflict: target of /a/x already exists with a different value (path: /b/x)
$ echo $?
1
```

含斜杠的键通过转义正确匹配：

```console
$ node src/cli.ts 'weird\/key/*' 'flat/$1' sample/tree.json
{ "a": { "x": 1, "y": 2 }, "b": { "y": 2 }, "weird/key": {}, "list": ["a","b","c"], "flat": { "sub": true } }
```

祖孙互移不循环（`a` → `a/b`）：

```console
$ node src/cli.ts 'a' 'a/b' sample/self.json
{ "a": { "b": { "x": 1 } } }
```

## 测试

```console
$ npm test
ℹ tests 22
ℹ pass 22
ℹ fail 0
```

覆盖：路径转义往返、无序对象/有序数组的相等性、基本迁移、幂等、三类目标冲突、斜杠键、数组保序与追加、`a`→`a/b` 自移、新节点不重扫、`**` 多段捕获与 `$N` 重排、外层优先、标量穿越报错、原型链同名键（`constructor`）安全、非法捕获引用、计划快照、输入不可变、CLI 端到端（含冲突退出码）。

## 已知限制

- 键名恰好为 `*` / `**` 时无法在模式中以字面量匹配；替换段整体为 `$N` 形式时一律按捕获引用处理。
- 新建中间容器的类型由目标下一段推断：整数形 → 数组，否则对象。
