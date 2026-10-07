# extract-function 重构器

输入带绑定身份（BindId）的纯函数表达式 AST，把指定子树提取为顶层函数。

## 设计

- **绑定身份**：每个绑定者（lambda 参数 / let 绑定 / 顶层函数）持有唯一 `BindId`，
  每个变量出现携带 `ref` 指向其绑定者。名字只是展示用的，全部分析基于 BindId，
  因此遮蔽与 alpha 改名不会干扰重构（`src/ast.ts`, `src/resolve.ts`）。
- **自由变量捕获**：`freeVarsOf` 按先序首次出现顺序返回子树的自由变量，
  子树内部引入的绑定绝不泄漏（`src/freevars.ts`）。
- **提取**：`extractFunction(root, targetId)` 生成闭的顶层函数（自由变量全部
  变为参数）、调用点（按参数顺序传参）与来源映射（每个原节点 → `function` /
  `root`，外加 `callSite.replaces`）（`src/extract.ts`）。
- **return 控制边界**：子树含有"最近外层 lambda 在子树之外"的 `return` 时拒绝提取。
- **核验**：`evaluate`（原树）与 `evaluateProgram`（重构树）两条独立入口；
  闭包带运行时身份，可观察捕获闭包穿过调用边界后仍是同一对象（`src/interp.ts`）。
- **性质测试**：随机程序上原树/重构树解释结果一致；随机 alpha 改名
  （名字池刻意极小以制造冲突）前后参数 BindId 集合不变（`test/refactor.test.ts`）。

## 运行

```sh
npm test   # node --test（Node >= 23，原生类型擦除运行 .ts）
```
