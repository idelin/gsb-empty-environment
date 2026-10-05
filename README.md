# minilang-rename-preflight

一个面向小型语言的重命名预检工具（TypeScript 实现，零依赖）。

## 语言（Minilang）

```text
let x = 1;                        // 顺序声明：声明点之后可见
fn add(a, b) {                    // 函数声明，体是闭包
  let c = a + b;
  return c;                       // 捕获定义处可见的外层变量
}
{ let y = add(x, 2); }            // 块作用域
export let foo = 1;               // 顶层导出
import { foo as bar } from "./other.ml";  // 导入与别名
```

语义要点：

- **块作用域**：`{ ... }` 与函数体引入新作用域，内层声明遮蔽外层。
- **顺序声明**：`let` / `fn` / 导入别名只在声明点之后可见；`let x = x;` 的初始化
  看不到自己。函数可以递归引用自身。
- **闭包捕获**：函数体内的引用只能绑定到函数**定义处**已可见的外层声明；
  定义之后在外层声明的变量对该函数不可见（解析为未绑定诊断）。
- **模块**：顶层 `export let/fn` 导出；`import { a as b }` 中 `a` 绑定到目标文件
  的导出符号，`b` 是本地别名符号；无别名时本地绑定名跟随导入名。

## API

```ts
import { analyzeProject, preflightRename, applyRenamePatch } from './src/index.ts';

const project = analyzeProject({ 'a.ml': src1, 'b.ml': src2 });
const result = preflightRename(project, 'a.ml#0', 'newName');
// result.ok / result.edits / result.conflicts / result.patch

if (result.ok) {
  const applied = applyRenamePatch(project, result.patch!);
  // applied.ok → applied.sources / applied.version / applied.project
}
```

- **符号 ID**：`文件路径#声明序号`（按源码顺序，重命名不改变序号，因此稳定）。
- **预检**：收集目标符号的声明与全部引用（含跨文件的导入说明符；无别名导入会
  连带本地绑定的引用），在内存副本上模拟改名、重新解析整个工程，逐引用比对
  前后绑定。任何绑定的变化都会作为冲突返回，冲突位置基于原始源码：
  - `capture`：改名后的引用会被其他同名声明捕获；
  - `shadow`：改名引入的声明会遮蔽其他符号的引用；
  - `captured-unbound` / `lost-binding` / `rebind`：未绑定变绑定、绑定丢失等；
  - `duplicate` / `import-missing-export` 等：改名引入的新诊断。
- **补丁**：`RenamePatch` 携带 `baseVersion`（全部源码的 SHA-256）。
  `applyRenamePatch` 先校验版本（源码已变则拒绝），应用后重新解析并复核
  所有引用绑定不变，然后返回新版本号。

## 运行测试

需要 Node ≥ 22.6（原生 TypeScript type-stripping；代码只使用可擦除语法）：

```sh
node --test 'tests/*.test.ts'
```

本机默认 node 为 v16，可用 codex 运行时自带的 Node 24：

```sh
~/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node --test 'tests/*.test.ts'
```

## 目录

- `src/lexer.ts` / `src/parser.ts`：词法、递归下降解析（标识符带源码顺序的稳定 id）
- `src/resolve.ts`：作用域树 + 顺序声明 + 闭包捕获边界的单遍解析器
- `src/project.ts`：多文件工程、导入链接、版本哈希
- `src/rename.ts`：重命名预检、冲突分类、补丁应用与复核
- `tests/`：闭包同名变量、声明前引用、多文件导入别名等 19 个用例
