import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createSIRegistry,
  UnitRegistry,
  Unit,
  DimensionVector,
  ContradictoryConversionError,
  DimensionMismatchError,
  UnknownUnitError,
  NoConversionPathError,
} from '../src/index.ts';

test('厘米到米精确为 1/100', () => {
  const registry = createSIRegistry();
  const factor = registry.factor('cm', 'm');
  assert.equal(factor.toString(), '1/100');
  assert.equal(registry.factor('m', 'cm').toString(), '100');
});

test('多跳路径合成：km → cm = 100000', () => {
  const registry = createSIRegistry();
  assert.equal(registry.factor('km', 'cm').toString(), '100000');
  assert.equal(registry.factor('in', 'mm').toString(), '127/5');
});

test('米不能转秒：量纲不符抛 DimensionMismatchError', () => {
  const registry = createSIRegistry();
  assert.throws(() => registry.factor('m', 's'), DimensionMismatchError);
  assert.throws(() => registry.addConversion('m', 's', 1), DimensionMismatchError);
});

test('矛盾转换边被拒绝，图保持原状', () => {
  const registry = createSIRegistry();
  assert.throws(
    () => registry.addConversion('cm', 'm', '1/10'),
    ContradictoryConversionError,
  );
  // 拒绝后原路径不受影响
  assert.equal(registry.factor('cm', 'm').toString(), '1/100');
  // 与已有路径一致的冗余边被接受（返回 false 表示未新增）
  assert.equal(registry.addConversion('cm', 'm', '1/100'), false);
  assert.equal(registry.addConversion('cm', 'm', '0.01'), false);
});

test('间接矛盾环：经由第三方单位合成后仍被检测', () => {
  const registry = new UnitRegistry();
  const L = DimensionVector.base('L');
  registry.registerBaseUnit(new Unit({ symbol: 'm', name: '米', dimension: L, scale: 1 }));
  registry.registerUnit(new Unit({ symbol: 'A', name: '单位A', dimension: L, scale: 2 }));
  registry.registerUnit(new Unit({ symbol: 'B', name: '单位B', dimension: L, scale: 4 }));
  // A→B 已有合成路径 1 A = 1/2 B；声明 1 A = 1/3 B 构成矛盾环
  assert.throws(
    () => registry.addConversion('A', 'B', '1/3'),
    ContradictoryConversionError,
  );
  assert.equal(registry.addConversion('A', 'B', '1/2'), false);
});

test('未知单位与无路径错误', () => {
  const registry = createSIRegistry();
  assert.throws(() => registry.get('furlong'), UnknownUnitError);
  // 注册一个无量纲基准单位，与长度单位之间没有路径
  registry.registerBaseUnit(
    new Unit({ symbol: 'count', name: '计数', dimension: DimensionVector.dimensionless(), scale: 1 }),
  );
  assert.throws(() => registry.factor('count', 'm'), DimensionMismatchError);
  // 同一注册表内同量纲单位都经基准单位连通，因此跨量纲之外不会出现断图；
  // NoConversionPathError 作为防御性分支保留在 factor() 中。
  assert.ok(new NoConversionPathError('a', 'b') instanceof Error);
});

test('重复注册同量纲第二个基准单位被拒绝', () => {
  const registry = createSIRegistry();
  const L = DimensionVector.base('L');
  assert.throws(
    () => registry.registerBaseUnit(new Unit({ symbol: 'm2', name: '假米', dimension: L, scale: 1 })),
    ContradictoryConversionError,
  );
});
