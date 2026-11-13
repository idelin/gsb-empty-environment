# Falco 规则条件字段与事件类型绑定检查

Falco 规则条件按事件类型过滤，字段只在特定事件下有值。攻击者对规则声明的事件类型
之外的系统调用动手，或利用内核 `proc.name`（task->comm）15 字节截断做不完整比较，
就能躲开检测。本包供规则管理服务在**规则加载前**调用，对条件 AST 做静态绑定检查，
拒绝或标记静默失效的规则。

## 检查项

- `never-matches`：某字段在声明的 `evt.type` 下恒为空（如 `fd.name` 对 `execve`），
  引用它的比较退化为恒假；若整个条件在该事件类型下恒假，报告规则对该事件永不匹配。
- `always-true`：`not` / `or` 与空字段组合使条件在某事件类型下恒真
  （如 `not (fd.name = x)` 在 `execve` 下恒真），规则会命中每一个事件，检测形同虚设。
- `proc-name-truncation`：`proc.name` 与超过 15 字节（按 UTF-8 字节计）的字面量做
  `=` / `==` 比较时报告——该比较永远不可能成立。
- 宏展开：循环引用直接抛 `MacroCycleError` 拒绝加载；嵌套深度上限 20，超出抛
  `MacroDepthError`；引用未定义宏抛 `UndefinedMacroError`。

恒真/恒假判定采用三值（Kleene）逻辑：字段不可用的比较为恒假，字段可用但值依赖运行期
的比较为 `unknown`，再按 `and` / `or` / `not` 向上传播。

## 用法

```ts
import { checkRuleCondition } from './index.ts';

const report = checkRuleCondition({
  condition: {
    kind: 'and',
    operands: [
      { kind: 'compare', field: 'proc.name', operator: '=', value: 'cat' },
      { kind: 'compare', field: 'fd.name', operator: 'contains', value: 'shadow' },
    ],
  },
  eventTypes: ['execve', 'openat'],
  fieldAvailability: {
    'fd.name': ['openat', 'open', 'unlinkat'],
    'proc.name': ['execve', 'openat', 'open', 'unlinkat'],
  },
  macros: {
    // 宏名 -> 条件 AST，支持嵌套引用
  },
});

if (!report.ok) {
  for (const finding of report.findings) console.error(finding.message);
}
// report.expandedCondition 为宏完全展开后的 AST
```

宏展开也可单独使用：

```ts
import { expandMacros, MacroCycleError } from './index.ts';

try {
  const expanded = expandMacros(conditionAst, macroTable);
} catch (error) {
  if (error instanceof MacroCycleError) {
    console.error(error.cycle); // 构成环的宏名链
  }
}
```

## 结构

- `src/types.ts`：条件 AST、字段映射、报告等公共类型
- `src/tribool.ts`：三值逻辑与逐事件类型求值
- `src/macros.ts`：宏展开、循环检测、深度上限（20）
- `src/proc-name.ts`：`proc.name` 15 字节截断检查
- `src/checker.ts`：`checkRuleCondition` 主入口
- `test/`：node:test 用例，覆盖 fd.name 配 execve、or 恒真、长进程名比较、宏环

## 测试

```sh
node --experimental-strip-types --test test/*.test.ts
```
