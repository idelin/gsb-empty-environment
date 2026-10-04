# minilang-rename-preflight

给一门含**块作用域、顺序声明、闭包捕获**的小语言（MinLang）实现的重命名预检工具。
给定符号 ID 与新名字，生成与源码版本绑定的重命名补丁；保证应用后所有引用仍绑定原符号，
否则会返回精确的冲突位置（捕获 / 遮蔽）。补丁应用后会重新解析并复核绑定不变量。

## 语言（MinLang）

```js
import { x as a, y } from "./other.lang";   // 多文件导入，支持别名与简写
let x = 1;                                   // 顺序声明：声明点之后才可见
fn f(n) {                                    // 函数声明（提升）+ 闭包捕获外层作用域
  let y = n;
  return x + y;
}
{
  let x = 2;                                 // 块作用域，遮蔽外层 x
  print(x);
}
x = x + 1;                                   // 赋值也是引用
```

语义要点：

- **顺序声明**：`let` / `import` 从声明语句起点开始对后续代码可见；声明之前的同名引用解析到外层作用域。同一作用域重复声明时，引用绑定到它之前最近的一个声明。
- **提升**：`fn` 声明与函数参数在整个作用域内可见。
- **闭包**：函数体形成新作用域，沿作用域链捕获外层符号。
- **导入**：`import { x as a }` 中 `x` 是对目标文件顶层符号的**引用**，`a` 是本地别名**声明**；简写 `import { y }` 两者共用一个 token。

## API

```ts
import { planRename, applyPatch, bindProject } from './src/index.ts';

const files = { 'main.lang': '...', 'other.lang': '...' };

// 1. 预检：返回 { ok: true, patch } 或 { ok: false, conflicts }
const plan = planRename(files, symbolId, 'newName');

// 2. 应用：校验版本哈希 -> 应用编辑 -> 重新解析 -> 复核全部绑定
if (plan.ok) {
  const result = applyPatch(files, plan.patch);
  // result.ok === true 时 result.sources 为重命名后的源码
}
```

- **符号 ID**：`"${file}:${声明标识符起始偏移}"`，通过 `bindProject` 拿到的 `Decl.id` 获得。
- **冲突类型**：
  - `captured` — 重命名会把别人名字的引用"抢"到新符号上；
  - `shadowed` — 指向被重命名符号的引用会被中间声明遮蔽，改绑到别的符号；
  - `rebound` — 其他绑定漂移（防御性兜底）。
  每个冲突带 `file / line / column / name / message`。
- **版本绑定**：`patch.version` 是全部源码的 SHA-1。`applyPatch` 先校验版本，
  源码被改动过则拒绝应用；通过后应用编辑、重新解析整个项目，并逐条复核
  "每个引用 → 声明"的绑定与打补丁前一致（位置经编辑偏移映射换算）。

## 实现要点

- 预检的冲突检测与打补丁后的复核共用同一条路径：**在内存中干跑（dry-run）应用编辑 →
  重新解析 → 比对绑定**。因此预检报告的冲突与真实应用后的复核结果必然一致。
- 简写导入是难点：重命名被简写导入的导出符号时，补丁把 `import { y }` 展开为
  `import { z as y }`，保持本地别名不变；此时别名声明 token 移到替换文本末尾，
  位置映射对"引用"和"声明"使用两套偏移规则（见 `Edit.localTokenLength`）。

## 运行

需要 Node.js ≥ 23.6（原生 TypeScript 类型擦除）：

```sh
node --test test/rename.test.ts   # 18 个测试
node examples/demo.ts             # 演示：冲突报告 + 干净重命名
```

测试覆盖：闭包内同名变量（改外层/改内层）、捕获冲突、遮蔽冲突、声明前引用、
顺序自捕获（`let b = a` 改 `a`→`b`）、多文件导入别名（改导出、改别名、简写展开）、
版本过期拒绝、非法新名字、未知符号。
