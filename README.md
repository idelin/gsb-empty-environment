# extract-fn — 纯函数表达式 AST 的子树提取重构工具

把带绑定身份（binding identity）的表达式 AST 中指定子树提取为顶层函数，
自动生成参数、调用点与来源映射，并附带独立解释器做等价性核验。

## 运行

```sh
bun test/run.ts   # 单元测试 + 随机属性测试
```

## 模块

- `src/core.ts` — AST 类型（每个绑定有唯一 `BindingId`，变量节点带解析后的 `ref`）、
  s-expr 解析器、名字 → 绑定身份的 resolver、打印机。
- `src/analyze.ts` — 自由变量分析：按先序**首次出现顺序**收集，以 `BindingId` 去重；
  子树内部的 `lambda`/`let` 绑定（含阴影变量）不会泄漏为自由变量。
- `src/extract.ts` — 提取器：
  - `crossesReturnBoundary`：子树含有"词法所属 lambda 在子树之外"的 `return` 时拒绝提取
    （`ExtractError`）；完全位于子树内 lambda 之下的 `return` 允许。
  - `extract(root, targetId)`：捕获变量按首次出现顺序转为参数（新鲜 `BindingId`，
    重名自动去重显示名）；深拷贝子树并把捕获引用重映射到新参数；
    生成 `let f = λparams. body' in <原树替换为 (f v1 ... vn)>`；
    调用点实参直接引用**原始** `BindingId`，因此被捕获的闭包在环境中保持对象身份。
  - `SourceMap`：生成节点 → 原节点（`originOf`）、原节点 → 生成节点（`generatedFrom`）、
    调用点 / 目标 / 函数根节点记录。
- `src/interp.ts` — 独立树遍历解释器：闭包为对象（身份可比较），`return` 用信号实现、
  在最近的 lambda 应用处捕获；`same?` 按对象身份比较闭包。
- `src/alpha.ts` — 随机 alpha 改名：只改绑定者的显示名，保留 `BindingId` 与节点 id。
- `src/random.ts` — 带类型的随机程序生成器（num/bool/num→num）与随机环境生成。

## 核验（test/run.ts）

- 单元测试：参数顺序、阴影捕获、局部不泄漏、闭包身份（`same? f g` 别名测试）、
  调用点传原始绑定、return 边界拒绝/放行、来源映射、根节点提取、alpha 稳定性。
- 属性测试：随机程序 × 每个节点 × 提取 → 独立解释原树与重构树比较结果
  （函数值外延比较）；同一节点在随机 alpha 改名后参数身份集合必须一致。
