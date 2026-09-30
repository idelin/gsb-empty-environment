import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSIRegistry, absolute, delta, DimensionMismatchError } from '../src/index.ts';

test('摄氏差量不加偏移：Δ10 °C = Δ10 K', () => {
  const registry = createSIRegistry();
  const d = delta(registry, 10, '°C').convertTo(registry.get('K'));
  assert.equal(d.value.toString(), '10');
  assert.equal(d.kind, 'delta');
  // 华氏差量只乘 5/9
  assert.equal(delta(registry, 9, '°F').convertTo(registry.get('K')).value.toString(), '5');
});

test('摄氏/华氏绝对量带偏移转换', () => {
  const registry = createSIRegistry();
  assert.equal(absolute(registry, 0, '°C').convertTo(registry.get('K')).value.toString(), '5463/20');
  assert.equal(absolute(registry, 0, '°C').convertTo(registry.get('K')).format(2), '273.15 K');
  assert.equal(absolute(registry, 32, '°F').convertTo(registry.get('°C')).value.toString(), '0');
  assert.equal(absolute(registry, 212, '°F').convertTo(registry.get('°C')).value.toString(), '100');
  assert.equal(absolute(registry, -40, '°C').convertTo(registry.get('°F')).value.toString(), '-40');
});

test('绝对量与差量的代数规则', () => {
  const registry = createSIRegistry();
  // 绝对量 − 绝对量 = 差量
  const diff = absolute(registry, 100, '°C').sub(absolute(registry, 0, '°C'));
  assert.equal(diff.kind, 'delta');
  assert.equal(diff.value.toString(), '100');
  assert.equal(diff.convertTo(registry.get('K')).value.toString(), '100');
  // 绝对量 + 差量 = 绝对量
  const raised = absolute(registry, 20, '°C').add(delta(registry, 5, 'K'));
  assert.equal(raised.kind, 'absolute');
  assert.equal(raised.value.toString(), '25');
  // 差量 ± 差量 = 差量
  assert.equal(delta(registry, 1, 'm').add(delta(registry, 1, 'cm')).value.toString(), '101/100');
  // 绝对量 + 绝对量：抛错
  assert.throws(
    () => absolute(registry, 1, 'm').add(absolute(registry, 1, 'm')),
    TypeError,
  );
  // 差量 − 绝对量：抛错
  assert.throws(() => delta(registry, 1, 'm').sub(absolute(registry, 1, 'm')), TypeError);
});

test('大数转换全程精确，不经 Number 中转', () => {
  const registry = createSIRegistry();
  const big = 12345678901234567890123456789012345678n;
  const inM = delta(registry, big, 'cm').convertTo(registry.get('m'));
  assert.equal(inM.value.toString(), '6172839450617283945061728394506172839/50');
  assert.equal(inM.format(0, 'down'), '123456789012345678901234567890123456 m (Δ)');
  assert.equal(inM.format(0, 'up'), '123456789012345678901234567890123457 m (Δ)');
});

test('跨量纲转换被拒绝（米 → 秒）', () => {
  const registry = createSIRegistry();
  assert.throws(
    () => delta(registry, 1, 'm').convertTo(registry.get('s')),
    DimensionMismatchError,
  );
});

test('厘米到米精确 1/100 且格式化需显式舍入', () => {
  const registry = createSIRegistry();
  const q = delta(registry, 1, 'cm').convertTo(registry.get('m'));
  assert.equal(q.value.toString(), '1/100');
  assert.equal(q.format(2), '0.01 m (Δ)');
  assert.equal(q.format(4, 'half-even'), '0.0100 m (Δ)');
});
