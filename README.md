# HM + Value Restriction + Row Polymorphism

一个可嵌入策略/查询编译器的 TypeScript Hindley-Milner 类型推导核，包含：

- `let` 多态与每次使用独立实例化；
- 严格语法值限制；
- 可扩展记录、字段投影、字段扩展、字段删除；
- Rémy 风格行合一、乱序字段匹配、`lacks` 约束传播；
- 普通类型变量与行变量的完整 occurs check；
- 稳定的规范化主类型输出。

## 快速使用

```ts
import {
  infer,
  inferScheme,
  parse,
  principalType,
  typeToString,
} from "ps-7ecc92cbbf";

principalType("let id = fn x => x in (id 1, id \"s\")");
// => "(Int, String)"

const scheme = inferScheme("fn r => r.x");
scheme.typeVars;  // quantified type variables
scheme.rowVars;   // quantified row variables + their lacks constraints
typeToString(scheme);
// => "forall 'a 'r. {x: 'a | 'r} -> 'a"

const type = infer(parse("(fn r => r.x) {b = \"s\", x = 1}"));
typeToString(type); // => "Int"
```

源码入口是 `src/index.ts`；包入口由 `tsconfig.build.json` 编译到 `dist/`。
`npm run build` 生成 JS 与 `.d.ts`，因此普通 Node、TypeScript 和 bundler
都可以直接导入包。

## 语言片段

解析器接受以下表达式形式：

```text
1, "text", 'x', true, ()
x
fn x => e
let x = e1 in e2
let rec f = fn x => e
ref e
!e
e1 := e2
e1; e2
{x = e1, y = e2}
{x = e1 | r}
{r \ x}
r.x
(e1, e2)
[e1, e2]
if e then e1 else e2
(e : T)
```

记录字段也接受 JSON 风格的 `{x: e1}`。预置环境包含 `nil`、`cons`、
`head`、`tail`、`isEmpty`、`ref`、`deref`、`:=`、算术/比较与布尔运算符。

类型语法示例：

```text
Int
String
Bool
Unit
'a -> 'b
{x: Int, y: String | r}
{ | r}
Ref<Int>
List<String>
(Int, Bool)
```

## 值限制

只有语法非扩张值才允许在 `let` 右侧泛化：

- 字面量、变量、lambda；
- 完全由语法值构成的记录、元组、列表；
- 带类型标注且其内部表达式是语法值。

应用、`ref e`、`!e`、`e1 := e2`、投影、删除、`let`、`if`、序列均不是
语法值。非值绑定的自由类型变量会被标记为 weak，后续不可泛化。因此：

```ts
infer(`
  let r = ref(nil) in
    (r := cons(1, nil); head(!r) + 'x')
`);
// throws HMTypeError with code "TYPE_MISMATCH"
```

同一个 `ref(nil)` 若错误地被泛化为 `forall 'a. Ref<List<'a>>`，上面的程序
会被错误接受；本实现会在整数写入后拒绝字符串加法。

## 行合一与 lacks

行被展开为 `label -> type` 的有序无关映射和可空行变量尾：

```text
{x: Int | rho}  =>  fields = {x: Int}, tail = rho
```

统一两个记录时会先匹配共有字段，再把只在单边出现的字段吸收到另一边的
行变量中。两边都有独有字段时会共享一个新的公共行尾。每次扩展都会向
行尾登记 `lacks` 约束；把受约束的行变量统一到包含禁用字段的行会抛出
`LACKS_VIOLATION`，重复字段会抛出 `DUPLICATE_FIELD`。

```ts
const row = freshRowVariable();
const open = recordType({}, row);
extendRow("x", intType, open.row); // row now lacks x
unify(open, recordType({x: intType})); // LACKS_VIOLATION
```

行变量自包含（如 `rho = {x: Int | rho}`）会抛出
`ROW_OCCURS_CHECK`；普通类型自包含（如 `'a = 'a -> Int`）会抛出
`OCCURS_CHECK`。

## 核心导出

- `parse(source)` / `parseType(source)`
- `infer(exprOrSource, env?)` → `Type`
- `inferScheme(exprOrSource, env?)` → `TypeScheme`
- `inferFull(...)` / `analyze(...)` → 类型、方案及字符串
- `principalType(exprOrSource, env?)` → 规范化主类型字符串
- `typeToString(typeOrScheme, options?)`
- `freshTypeVariable(...)`、`freshRowVariable(...)`、`recordType(...)`
- `unifyTypes(...)`、`unifyRows(...)`、`occursInType(...)`、`occursInRow(...)`
- `generalize(...)`、`instantiate(...)`、`isNonExpansive(...)`
- AST 构造器：`int`、`string`、`bool`、`variable`、`lambda`、`apply`、
  `letIn`、`record`、`project`、`remove`、`ref`、`deref`、`assign` 等

错误统一为 `HMTypeError`，可用 `error.code` 判断
`TYPE_MISMATCH`、`OCCURS_CHECK`、`ROW_OCCURS_CHECK`、
`DUPLICATE_FIELD`、`LACKS_VIOLATION`、`UNBOUND_VARIABLE` 等类别。

## 规范化输出

记录字段按标签排序，类型变量按首次出现顺序命名，行变量与类型变量共享
不冲突的命名空间：

```text
fn r => r.x
=> forall 'a 'r. {x: 'a | 'r} -> 'a

fn r => {y = 1 | r}
=> forall 'r. { | 'r} -> {y: Int | 'r}

fn r => {r \ x}
=> forall 'a 'r. {x: 'a | 'r} -> { | 'r}
```

`typeToString(scheme, { showLacks: true })` 会追加方案持有的残余
`lacks(row, {fields})` 约束。

## 验证

```bash
npm test       # 编译并运行 test/*.test.{js,mjs}
npm run typecheck
```
