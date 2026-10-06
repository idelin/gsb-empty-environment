# rule-tree-matcher

规则树模式匹配库(TypeScript)。规则树由原子(`string | number | boolean | null`)
与列表组成;模式支持三类节点:

- **字面节点** `lit(a)`:匹配与 `a` 结构相等的原子。
- **单节点变量** `v('x')`:匹配任意一个节点(原子或列表)并绑定。
- **尾部变量** `tail('xs')`:匹配列表剩余元素(切片,**允许为空**),
  只允许出现在列表末尾,构造非法模式时 `validatePattern` 抛错。

## 语义

- **同名变量一致性**:同名变量(或同名尾部变量)每次出现必须绑定结构相等
  的值,否则匹配在该次出现处失败。同一名字不允许既作单节点变量又作尾部变量。
- **稳定绑定**:成功时返回 `bindings`,每个名字一条,按模式中首次出现
  (深度优先、从左到右)的顺序排列;匹配过程确定性,重复运行结果一致。
- **最浅失败路径**:失败时返回 `path`(从根开始的下标路径)。长度不匹配在
  列表自身处报告,节点不匹配在出错节点处报告,重复变量冲突在后一次出现处
  报告。
- **代入**: `substitute(pattern, bindings)` 把绑定代回模式(尾部变量就地
  展开)。对任意成功的 `match(pattern, subject)`,代入结果与 `subject`
  结构相等。

## 示例

```ts
import { lit, list, match, substitute, tail, v } from './src';

const r = match(list(lit('a'), v('x'), tail('rest')), ['a', ['b'], 'c', 'c']);
// r.ok === true
// r.bindings === [{ name: 'x', value: ['b'] }, { name: 'rest', value: ['c', 'c'] }]
// substitute(pattern, r.bindings) 深等于原 subject

match(list(v('x'), v('x')), ['a', 'b']);
// { ok: false, path: [1], reason: 'variable "x" re-bound ...' }

match(list(lit('a'), tail('rest')), ['a']);
// 空尾合法: rest 绑定 []
```

## 验证

`test/targeted.ts` 为定向单测(重复变量、嵌套尾部、空尾、最浅失败路径、
绑定顺序、校验抛错)。`test/exhaustive.ts` 穷举所有规模 ≤ 5 的小树与
规模 ≤ 4 的模式(291 × 106 组),对每一组断言:

1. 匹配结果确定性(两次运行一致);
2. 成功时代入后相等(`substitute` 结果 ≡ subject),绑定顺序稳定,
   重复变量的各次出现绑定结构相等的值;
3. 成功/失败与独立暴力预言机(枚举全部候选赋值做代入检查)结论一致;
4. 失败路径可定位、原因非空。

## 构建与测试

需要 TypeScript ≥ 5(`tsc` 在 PATH 上):

```sh
npm test        # tsc -p tsconfig.json && node out/test/run.js
```
