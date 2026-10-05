# semantic-token-index

面向语义高亮的**版本化语义 token 索引**：每次源码更新重新分析作用域绑定，
并返回相邻版本之间的**最小区间替换列表**（patches）。声明的修改（重命名、
删除、新增遮蔽声明）可能改变远处同名引用的绑定，这些远端变化会精确体现
在增量补丁中。

## 设计

- **Unicode 标量存储**：`ScalarText`（`src/scalar-text.ts`）把源码存为
  Unicode 标量值（码点）序列，并提供双向映射
  `scalarOffsetToUtf16` / `utf16OffsetToScalar`（落在代理对中间会抛错），
  以及 `(line, character)` 位置换算，列以 UTF-16 为单位。
- **作用域分析**：`analyzeSource`（`src/analyzer.ts`）解析一个小型语句
  语言（`let` / `fn` / 块 / 表达式），按词法作用域解析绑定（无提升，
  内层遮蔽外层），为每个标识符出现位置产出一个语义 token。token 位置为
  UTF-16 偏移，保证有序且不重叠（`assertValidTokens` 校验）。
- **绑定标识**：`binding` 形如 `name@scalarOffset`，由内容派生，跨版本
  可比较——同一位置同名声明视为同一绑定。
- **最小补丁**：`computePatches`（`src/diff.ts`）用 Myers O(ND) 差分求
  最小编辑脚本，保留块之间的间隙合并为替换区间 `{start, end, tokens}`。
  区间按 `start` 排序、互不重叠，作用于旧 token 数组的下标。
- **版本索引**：`SemanticTokenIndex`
  （`src/semantic-token-index.ts`）保存不可变快照历史；`update(source)`
  返回 `{fromVersion, toVersion, patches}`；`delta(v)` 取相邻版本补丁；
  `SemanticTokenIndex.applyPatches(base, patches)` 应用补丁并校验结果。
  不变式：对任意相邻版本，`applyPatches(tokens(v), delta(v).patches)`
  与 `tokens(v+1)` 全量结果完全一致。

注意：token 使用绝对 UTF-16 偏移，因此在文本中间插入内容会使其后所有
token 位置平移，补丁区间会覆盖这些 token（这是位置编码的固有性质，而非
绑定变化）。等长修改（如重命名）才能体现真正"最小"的远端绑定更新。

## 语言文法

```
program    := statement*
statement  := "let" IDENT "=" expr ";"          // 变量声明（初始化表达式在旧作用域解析）
            | "fn" IDENT "(" params ")" block   // 函数声明，参数为函数作用域内声明
            | "{" statement* "}"                // 块作用域
            | expr ";"
expr       := primary (binop primary)*
primary    := IDENT | NUMBER | STRING | "(" expr ")" | primary "(" args ")"
```

标识符支持 Unicode（`ID_Start`/`ID_Continue`）；注释支持 `//` 与 `/* */`。

## 运行测试

需要 Node.js >= 22.6（原生 TypeScript 类型擦除）。仓库仅使用可擦除语法
（无 enum / 参数属性），无需编译步骤。

```sh
npm test            # 自动选择可用的 node（PATH 或 nvm）
# 或手动：
node --test test/*.test.ts        # node >= 23.6
node --experimental-strip-types --test test/*.test.ts   # node 22.6+
```

## API 示例

```ts
import { SemanticTokenIndex } from './src/index.ts';

const index = new SemanticTokenIndex();
index.update('let x = 1;\n{ let y = x; }\nx;\n');
const delta = index.update('let z = 1;\n{ let y = x; }\nx;\n');
// delta.patches === 两个区间：声明处 + 两处远端引用（变为 unresolved）
const rebuilt = SemanticTokenIndex.applyPatches(index.tokens(0), delta.patches);
// rebuilt 与 index.tokens(1) 完全一致
```
