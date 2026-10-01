/**
 * 运行样例：累进佣金 + 整档服务费叠加，带段最低/最高、折扣、全局上下限与两级舍入。
 * 纯数值函数演示，不连接任何真实订单或计费服务。
 * 运行：node examples/demo.ts   （需要 Node >= 23.6）
 */
import { parse, toDisplayString } from '../src/rational.ts';
import { evaluate, formatTrace } from '../src/evaluate.ts';
import { compile } from '../src/compile.ts';
import { invert } from '../src/invert.ts';
import { intervalToString } from '../src/interval.ts';
import type { CompositeSpec } from '../src/rules.ts';

const spec: CompositeSpec = {
  rules: [
    {
      name: '累进佣金',
      mode: 'progressive', // 累进分段：每档只对自己覆盖的部分计费
      segmentRounding: { quantum: parse('0.01'), mode: 'half-up' }, // 各段独立舍入到分
      discount: parse('9/10'), // 整规则九折
      brackets: [
        { lower: { value: null, closed: false }, upper: { value: parse('0'), closed: false }, rate: parse('0') },
        { lower: { value: parse('0'), closed: true }, upper: { value: parse('10000'), closed: false }, rate: parse('0.02') },
        { lower: { value: parse('10000'), closed: true }, upper: { value: parse('50000'), closed: false }, rate: parse('0.03') },
        { lower: { value: parse('50000'), closed: true }, upper: { value: null, closed: false }, rate: parse('0.05'), max: parse('2000') }, // 段封顶 2000
      ],
    },
    {
      name: '物流整档',
      mode: 'whole', // 整量落档：按 x 所落档位整体计费
      brackets: [
        { lower: { value: null, closed: false }, upper: { value: parse('0'), closed: false }, fixed: parse('0') },
        { lower: { value: parse('0'), closed: true }, upper: { value: parse('20000'), closed: false }, fixed: parse('120') },
        { lower: { value: parse('20000'), closed: true }, upper: { value: null, closed: false }, fixed: parse('200'), min: parse('200') },
      ],
    },
  ],
  min: parse('0'), // 全局最低 0（退货为负时兜底）
  max: parse('3000'), // 全局最高 3000
  finalRounding: { quantum: parse('0.01'), mode: 'half-even' }, // 最终总额舍入，与段舍入独立
};

console.log('=== 逐段贡献证据 ===');
for (const x of ['7500', '20000', '123456.78', '-100']) {
  console.log(formatTrace(evaluate(spec, parse(x))));
  console.log('');
}

console.log('=== 恰阈值不双计（x=20000 为物流档位边界）===');
const t = evaluate(spec, parse('20000'));
console.log(`物流规则命中段数=${t.rules[1].segments.length}，命中档=${t.rules[1].segments[0].bracket}`);
console.log('');

const compiled = compile(spec);
console.log('=== 编译后的精确分段函数 ===');
console.log(`断点: ${compiled.breakpoints.map(toDisplayString).join(', ')}  分段数: ${compiled.pieces.length}`);
console.log('');

console.log('=== 反求：给定输出求所有输入区间 ===');
const x0 = parse('23456.78');
const y0 = evaluate(spec, x0).output;
console.log(`先算得 f(${toDisplayString(x0)}) = ${toDisplayString(y0)}，再反求 f(x) = ${toDisplayString(y0)}：`);
for (const iv of invert(compiled, y0)) console.log(`  ${intervalToString(iv)}`);
console.log('');
console.log('=== 反求：不可达输出 f(x) = 0.005（非舍入量子倍数）===');
console.log(`  解集: ${invert(compiled, parse('0.005')).map(intervalToString).join(' ∪ ') || '∅'}`);
console.log('');

console.log('=== 平台多解（整档阶梯价，两个平台高度相同）===');
const platform: CompositeSpec = {
  rules: [
    {
      name: '阶梯价',
      mode: 'whole',
      brackets: [
        { lower: { value: null, closed: false }, upper: { value: parse('-5'), closed: false }, rate: parse('-1') },
        { lower: { value: parse('-5'), closed: true }, upper: { value: parse('0'), closed: true }, rate: parse('0'), fixed: parse('5') },
        { lower: { value: parse('0'), closed: false }, upper: { value: parse('5'), closed: false }, rate: parse('1') },
        { lower: { value: parse('5'), closed: true }, upper: { value: parse('10'), closed: true }, rate: parse('0'), fixed: parse('5') },
        { lower: { value: parse('10'), closed: false }, upper: { value: null, closed: false }, rate: parse('1'), fixed: parse('-5') },
      ],
    },
  ],
};
const pc = compile(platform);
console.log(`f(x)=5 的解: ${invert(pc, parse('5')).map(intervalToString).join(' ∪ ')}`);
console.log(`f(x)=7 的解: ${invert(pc, parse('7')).map(intervalToString).join(' ∪ ')}`);
