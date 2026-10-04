# 位向量表达式等价检查器（bv-equiv-checker）

检查两个表达式在给定位宽下是否对所有输入等价。支持按位运算、加减、移位和
条件选择，所有结果按给定位宽截断（无符号回绕）。

## 语义

- **截断**：每个运算结果都对位宽 `w` 取模（`mod 2^w`），常量与移位量字面量
  同样按 `w` 位截断。
- **移位**：`<<` 为逻辑左移，`>>` 为逻辑右移。移位量大于位宽时结果固定为 0
  （移位量等于位宽时逻辑移位结果自然为 0，因此行为对所有输入都是确定的）。
- **条件选择**：`c ? a : b`，条件按「非零」判断。
- **反例顺序**：变量按名字字典序排列成输入元组，各变量按无符号值比较；
  非等价时返回该顺序下最小的输入反例。

## 表达式语法

```
ite   := bitor ('?' ite ':' ite)?
bitor := bitxor ('|' bitxor)*      bitxor := bitand ('^' bitand)*
bitand:= shift ('&' shift)*        shift  := addsub (('<<'|'>>') addsub)*
addsub:= unary (('+'|'-') unary)*  unary  := ('~'|'-') unary | atom
atom  := number | ident | '(' ite ')'
```

数字支持十进制、`0x` 十六进制、`0b` 二进制；标识符为 `[A-Za-z_][A-Za-z0-9_]*`。

## 引擎

- **symbolic**（符号约束）：把两个表达式经 Tseitin 变换位爆炸为 CNF，
  构造 miter（`A XOR B ≠ 0`）交给内置 CDCL SAT 求解器（1-UIP 子句学习、
  VSIDS、相位保存、重启）。UNSAT 即等价证明；SAT 则逐变量、逐位（MSB 优先）
  用假设文字最小化出字典序最小反例。
- **exhaustive**（独立解释器）：BigInt 参考解释器对全部输入空间逐一求值，
  是小位宽下的完整证明（而非抽样）。枚举顺序即字典序，首个失配点即最小反例。
- **auto**（默认）：输入总位数 ≤ 20 时用穷举证明，否则用符号引擎。

两个引擎互相独立：测试中用穷举结果交叉验证符号引擎（见下文）。

## 预算与 unknown

SAT 求解器带冲突数 / 传播步数预算（`--budget-conflicts`、`--budget-props`），
穷举引擎带输入空间上限。预算耗尽返回 `unknown`，绝不猜测结论。

## 使用

需要 Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需构建步骤）。

```sh
# 检查等价性（退出码：0 等价，1 不等价，2 未知，3 用法/解析错误）
node src/cli.ts --width 8 '(x + y) - y' 'x'
node src/cli.ts --width 4 --engine symbolic 'x << y' 'x << (y & 1)'

# 运行测试
npm test        # 或 node --test
```

库用法：

```ts
import { parse } from './src/parser.ts';
import { checkEquivalence } from './src/checker.ts';

const r = checkEquivalence(parse('x & y'), parse('x | y'), { width: 4 });
// { status: 'not-equal', counterexample: { x: 0n, y: 1n } }
```

## 验证策略

- 小位宽穷举即证明：`test/crosscheck.test.ts` 系统性枚举表达式语料
  （全部二元/一元运算、移位、条件选择的组合），在宽度 1–5 下要求符号引擎
  与穷举证明的状态和最小反例完全一致。
- SAT 求解器本身用确定性生成的 CNF 实例对全枚举真值表校验
  （`test/sat.test.ts`）。
- 符号引擎给出的每个反例都会先经独立解释器复核后才返回。

## 结构

- `src/ast.ts` — 表达式 AST 与变量收集
- `src/parser.ts` — 表达式解析器
- `src/interp.ts` — 独立 BigInt 解释器（参考语义）
- `src/cnf.ts` / `src/bv.ts` — Tseitin 门电路与位向量电路（加法器、桶形移位器、mux）
- `src/sat.ts` — CDCL SAT 求解器（学习、VSIDS、假设、预算）
- `src/bitblast.ts` — 表达式到位爆炸
- `src/checker.ts` — 符号等价检查 + 最小反例提取 + 引擎选择
- `src/exhaustive.ts` — 穷举检查器
- `src/cli.ts` — 命令行入口
