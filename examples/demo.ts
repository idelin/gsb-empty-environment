import {
  createSIRegistry,
  absolute,
  delta,
  Rational,
  ContradictoryConversionError,
  DimensionMismatchError,
} from '../src/index.ts';

const registry = createSIRegistry();
const show = (label: string, value: unknown) => console.log(`${label.padEnd(46)} ${value}`);

console.log('=== 1. 精确换算：厘米 → 米，严格 1/100 ===');
const oneCm = delta(registry, 1, 'cm').convertTo(registry.get('m'));
show('1 cm → m（精确分数）', oneCm.toString());
show('1 cm → m（十进制）', oneCm.format(2));

console.log('\n=== 2. 大数不经过 Number 中转 ===');
const big = 12345678901234567890123456789012345678n;
show('原始 BigInt（cm）', big.toString());
show('→ m（精确分数）', delta(registry, big, 'cm').convertTo(registry.get('m')).toString());
show('→ km（精确十进制）', delta(registry, big, 'cm').convertTo(registry.get('km')).format(0, 'down'));

console.log('\n=== 3. 显式舍入规则（1/3 与 2.5）===');
const third = Rational.of('1/3');
show('1/3, 4 位, half-even', third.toDecimalString(4, 'half-even'));
show('1/3, 4 位, up', third.toDecimalString(4, 'up'));
show('1/3, 4 位, down', third.toDecimalString(4, 'down'));
show('2.5, 0 位, half-even（银行家）', Rational.of('2.5').toDecimalString(0, 'half-even'));
show('2.5, 0 位, half-up', Rational.of('2.5').toDecimalString(0, 'half-up'));
show('-2.5, 0 位, floor', Rational.of('-2.5').toDecimalString(0, 'floor'));
show('-2.5, 0 位, ceiling', Rational.of('-2.5').toDecimalString(0, 'ceiling'));

console.log('\n=== 4. 绝对量 vs 差量：摄氏度的偏移 ===');
show('绝对量 0 °C → K', absolute(registry, 0, '°C').convertTo(registry.get('K')).toString());
show('差量 Δ10 °C → K（不加偏移）', delta(registry, 10, '°C').convertTo(registry.get('K')).toString());
show('绝对量 32 °F → °C', absolute(registry, 32, '°F').convertTo(registry.get('°C')).toString());
show('绝对量 212 °F → °C', absolute(registry, 212, '°F').convertTo(registry.get('°C')).toString());
show('100 °C − 0 °C = 差量', absolute(registry, 100, '°C').sub(absolute(registry, 0, '°C')).toString());

console.log('\n=== 5. 转换图：多跳路径与矛盾检测 ===');
show('1 km → cm（多跳合成）', delta(registry, 1, 'km').convertTo(registry.get('cm')).toString());
try {
  registry.addConversion('cm', 'm', '1/10');
} catch (e) {
  if (e instanceof ContradictoryConversionError) show('矛盾边 cm→m=1/10 被拒绝', e.message);
}
try {
  registry.addConversion('m', 's', 1);
} catch (e) {
  if (e instanceof DimensionMismatchError) show('量纲不符 m→s 被拒绝', e.message);
}
show('一致冗余边 cm→m=1/100（返回 false）', registry.addConversion('cm', 'm', '1/100'));

console.log('\n=== 6. 类型规则：绝对量 + 绝对量 抛错 ===');
try {
  absolute(registry, 1, 'm').add(absolute(registry, 1, 'm'));
} catch (e) {
  show('1 m + 1 m（绝对量）', (e as Error).message);
}
show('1 m + Δ1 cm（绝对量+差量）', absolute(registry, 1, 'm').add(delta(registry, 1, 'cm')).toString());
