import { ExactWindowStats } from '../src/stats.ts';
import { makeRational, toDecimal, toFraction } from '../src/rational.ts';
import type { Rational } from '../src/rational.ts';

function show(label: string, r: Rational | null, places = 6): void {
  console.log(
    `  ${label}: ${r === null ? 'undefined' : `${toFraction(r)}  ≈ ${toDecimal(r, places, 'half-even')}`}`,
  );
}

// 1) 大偏移 + 小方差：1e30 附近相差 1，浮点会灾难消减，这里精确
console.log('== 大偏移小方差（1e30 附近 ±1） ==');
const w = new ExactWindowStats({ maxSize: 3 });
w.add('1000000000000000000000000000000');
w.add('1000000000000000000000000000001');
w.add('1000000000000000000000000000002');
show('sum', w.sum(), 0);
show('mean', w.mean(), 0);
show('sample variance', w.variance('sample'));
show('population variance', w.variance('population'));

// 2) 滑窗增删 + 重复值
console.log('\n== 滑窗（maxSize=3）加入重复值并删除 ==');
const a = w.add('1000000000000000000000000000001'); // 驱逐最旧值
console.log('  加入重复值后 count =', w.count);
w.remove(a);
console.log('  删除该重复值后 count =', w.count);
show('sum', w.sum(), 0);
show('sample variance', w.variance('sample'));

// 3) undo 恢复原统计
console.log('\n== undo 恢复 ==');
const before = w.snapshot();
const tmp = w.add('999');
w.remove(tmp);
w.add('123');
w.undo(); // 撤销 add('123')
w.undo(); // 撤销 remove(tmp)
w.undo(); // 撤销 add(tmp)
const after = w.snapshot();
console.log('  三次 undo 后与之前快照一致:',
  toFraction(after.sum) === toFraction(before.sum) &&
  toFraction(after.sampleVariance!) === toFraction(before.sampleVariance!));

// 4) n=1 时样本方差未定义
console.log('\n== n=1 样本方差未定义 ==');
const single = new ExactWindowStats();
single.add('0.1');
show('mean', single.mean());
show('sample variance', single.variance('sample'));
show('population variance', single.variance('population'));

// 5) 显式舍入
console.log('\n== 显式舍入规则（1/3 与 0.125，保留 2 位小数） ==');
for (const mode of ['down', 'up', 'floor', 'ceil', 'half-up', 'half-even'] as const) {
  console.log(`  ${mode.padEnd(9)} 1/3 → ${toDecimal(makeRational(1n, 3n), 2, mode)}   0.125 → ${toDecimal(makeRational(1n, 8n), 2, mode)}`);
}
