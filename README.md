# LR(1) 分析表构建与冲突检测诊断器

TypeScript 实现，零依赖，直接使用 Node.js（>= 22.6，类型擦除）运行与测试。

## 功能

- First 集 / Follow 集不动点计算（支持空产生式）
- LR(1) 项集闭包（closure）与 GOTO 构造，生成规范 LR(1) 项集族
- ACTION / GOTO 分析表生成
- 精确定位 Shift/Reduce 与 Reduce/Reduce 冲突（状态号 + 向前看符号 + 冲突动作）
- 输出导致冲突的**最短输入 Token 串证据**（自动机上 BFS + 非终结符最短终结展开）
- 生成含冲突状态完整项目集与解决建议的诊断报告

## 运行

```bash
npm run demo   # 对经典 Dangling-Else 文法输出完整诊断报告
npm test       # Node.js 内置测试运行器（node:test）
```

## 样例输出（Dangling-Else）

```
冲突 #1: shift/reduce 冲突 （状态 14，向前看符号 'else'）

  状态 14 的 LR(1) 项目集：
    [S -> if E then S • else S, $]
    [S -> if E then S •, $]
    [S -> if E then S • else S, else]
    [S -> if E then S •, else]

  遇到 'else' 时以下动作均合法：
    - shift -> state 15
    - reduce by S -> if E then S

  最短冲突证据输入串： if e then if e then other else
```

## 测试结果

9 个测试全部通过，覆盖：First/Follow 计算（含空产生式）、Dangling-Else
Shift/Reduce 冲突检测与证据串、无二义表达式文法零冲突、Reduce/Reduce 冲突检测。

## 代码结构

- `src/grammar.ts` — 文法表示、First/Follow 集计算
- `src/lr1.ts` — LR(1) 闭包、项集族与分析表构建、冲突收集
- `src/diagnose.ts` — 最短冲突证据串与诊断报告生成
- `src/demo.ts` — Dangling-Else 演示入口
- `test/lr1.test.ts` — node:test 测试
