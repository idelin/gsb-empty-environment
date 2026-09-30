import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Rat } from '../src/rational.ts';
import { U, UnitMismatchError, convertValue, divUnit, mulUnit } from '../src/units.ts';

describe('单位系统', () => {
  it('同维度精确换算', () => {
    const v = convertValue(Rat.parse('1.005'), U.m, U.cm);
    assert.equal(v.toString(), '201/2'); // 100.5，精确
    assert.equal(convertValue(Rat.of(2n), U.min, U.s).toString(), '120');
  });

  it('维度不同直接抛错，不可换算', () => {
    assert.throws(() => convertValue(Rat.of(1n), U.m, U.s), UnitMismatchError);
    assert.throws(() => convertValue(Rat.of(1n), U.usd, U.m), UnitMismatchError);
  });

  it('乘除运算传播维度与因子', () => {
    const speed = divUnit(U.m, U.s);
    assert.equal(speed.dimsKey(), 'L^1,T^-1');
    const area = mulUnit(U.cm, U.cm);
    assert.equal(area.factor.toString(), '1/10000');
    assert.ok(mulUnit(U.m, U.m).sameDim(area));
    assert.ok(!mulUnit(U.m, U.m).same(area)); // 因子不同 → 单位口径不同
  });

  it('货币维度独立于长度', () => {
    assert.ok(!U.usd.sameDim(U.m));
    assert.ok(U.cent.sameDim(U.usd));
    assert.ok(!U.cent.same(U.usd));
  });
});
