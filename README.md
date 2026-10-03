# Attribute Grammar Evaluator（带属性文法求值器）

一个语法制导翻译（SDT）内核：在 AST 生成之后，把**综合属性（synthesized）**与
**继承属性（inherited）**的语义规则实例化为属性依赖图，做**拓扑排序**后按序求值，
并**检测属性计算的循环依赖**。内置一个演示语言，实现变量声明/使用的类型推导、
自顶向下传递的作用域符号表环境，以及带精确 AST 节点位置（行:列）的类型错误报告。

## 结构

- `src/ast.ts` — 通用 AST 节点（`kind` / `pos` / `fields` / 子节点），携带源码位置。
- `src/grammar.ts` — 与语言无关的属性文法框架：
  - `SemanticRule`：声明属性名、种类（综合/继承）、静态依赖 `dependsOn` 与计算函数 `compute`；
  - 依赖用相对寻址表达：`self` / `parent` / `child(i)` / `sibling(offset)`；
  - `Evaluator`：实例化全部属性 → 建依赖图 → Kahn 拓扑排序 → 按序求值；
    剩余未排序节点即环，DFS 提取具体环路径并抛出 `CycleError`；
  - 引用未定义属性抛出 `UndefinedAttributeError`；诊断信息按源码位置排序输出。
- `src/lang.ts` — 演示语言（`let x: int = 1 + 2;` 风格的语句序列）的属性文法：
  - 继承属性 `env`：符号表环境自顶向下传递，语句间经 `sibling(-1).envOut` 链式穿线；
  - 综合属性 `type` / `value`：表达式类型与计算结果自底向上返回；
  - 综合属性 `envOut`：`VarDecl` 把「名字 → {类型, 值}」加入环境传给后续语句；
  - 综合属性 `check`：声明类型与初始化表达式类型不匹配时在声明节点位置报错。
- `src/demo.ts` — 可运行样例（良类型程序 / 类型错误定位 / 循环依赖检测）。
- `test/` — 基于 Node.js 内置 `node:test` 的测试。

## 运行

```bash
npm test    # 编译 + 运行全部测试（node --test）
npm run demo  # 编译 + 运行样例
```

要求：Node.js ≥ 18（`node:test`），TypeScript 5（`npm i -D typescript @types/node`；
本仓库在离线环境下已将二者预置在 `node_modules/` 中）。

## 样例输出（`npm run demo`）

```
=== Sample 1: well-typed program ===
declared x: env entry = { type: 'int', value: 3 }
declared y: env entry = { type: 'bool', value: true }
expr stmt @3: value = 12
expr stmt @4: value = true
diagnostics: (none)

=== Sample 2: type errors ===
error at 1:5 type mismatch: cannot initialize variable 'a' of type 'int' with expression of type 'bool'
error at 3:3 type mismatch: operator '+' expects (int, int) but got (int, bool)
error at 4:1 undefined variable 'missing'

=== Sample 3: cycle detection ===
detected as expected: circular attribute dependency detected: Node#28.a -> Node#28.b -> Node#28.a
```

## 测试结果（`npm test`，Node v22.14.0）

```
# tests 12
# pass 12
# fail 0
```

覆盖：综合属性自底向上拓扑求值顺序、继承属性自顶向下传播、兄弟节点链式穿线、
节点内与跨节点（综合↔继承）循环依赖检测、未定义属性快速失败、变量声明/使用类型推导、
声明处/运算符处/未定义变量处的精确位置报错、声明前不可见的作用域顺序。
