import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Rat, roundRat } from '../src/rational.ts';

describe('Rat 精确有理数', () => {
  it('约分与符号规范化', () => {
    assert.equal(Rat.of(6n, -8n).toString(), '-3/4');
    assert.equal(Rat.of(10n, 5n).toString(), '2');
    assert.equal(Rat.of(0n, 7n).toString(), '0');
  });

  it('解析十进制与分式字符串（精确）', () => {
    assert.equal(Rat.parse('0.125').toString(), '1/8');
    assert.equal(Rat.parse('-2.50').toString(), '-5/2');
    assert.equal(Rat.parse('3/7').toString(), '3/7');
  });

  it('大数运算不经过 Number，保持精确', () => {
    const big = Rat.of(9007199254740993n); // 超过 Number.MAX_SAFE_INTEGER
    const sum = big.add(Rat.of(1n, 3n));
    assert.equal(sum.toString(), '27021597764222980/3');
    // 若经 Number 中转，9007199254740993 会被截为 9007199254740992
    assert.notEqual(sum.n % 2n === 0n && sum.d === 1n, true);
  });

  it('四则运算精确', () => {
    const a = Rat.parse('0.1');
    const b = Rat.parse('0.2');
    assert.equal(a.add(b).toString(), '3/10'); // 二进制浮点会得到 0.30000000000000004
    assert.equal(Rat.of(1n, 3n).mul(Rat.of(3n)).toString(), '1');
    assert.equal(Rat.of(1n, 2n).div(Rat.of(1n, 4n)).toString(), '2');
  });

  it('有限小数精确展开，无限循环不近似', () => {
    assert.equal(Rat.of(1n, 8n).toDecimalExact(), '0.125');
    assert.equal(Rat.of(1n, 3n).toDecimalExact(), null);
    assert.equal(Rat.of(1n, 3n).display(), '1/3');
  });

  it('拒绝不安全的 Number 输入', () => {
    assert.throws(() => Rat.of(2 ** 53), /拒绝不安全的 Number/);
    assert.throws(() => Rat.of(0.1), /拒绝不安全的 Number/);
  });
});

describe('roundRat 显式舍入规则', () => {
  const x = Rat.parse('0.125');
  it('half-up：半数远离零', () => {
    assert.equal(roundRat(x, 2, 'half-up').toString(), '13/100');
    assert.equal(roundRat(x.neg(), 2, 'half-up').toString(), '-13/100');
  });
  it('half-even：半数向偶', () => {
    assert.equal(roundRat(x, 2, 'half-even').toString(), '3/25'); // 0.12
    assert.equal(roundRat(Rat.parse('0.135'), 2, 'half-even').toString(), '7/50'); // 0.14
  });
  it('half-down：半数朝向零', () => {
    assert.equal(roundRat(x, 2, 'half-down').toString(), '3/25');
    assert.equal(roundRat(Rat.parse('0.1251'), 2, 'half-down').toString(), '13/100');
  });
  it('down/up/floor/ceil 在负数上的差异', () => {
    const y = Rat.parse('-1.234');
    assert.equal(roundRat(y, 2, 'down').toString(), '-123/100');
    assert.equal(roundRat(y, 2, 'up').toString(), '-31/25');
    assert.equal(roundRat(y, 2, 'floor').toString(), '-31/25');
    assert.equal(roundRat(y, 2, 'ceil').toString(), '-123/100');
  });
  it('scale=0 与已精确值', () => {
    assert.equal(roundRat(Rat.parse('9.95'), 0, 'half-up').toString(), '10');
    assert.equal(roundRat(Rat.of(7n), 2, 'half-up').toString(), '7');
  });
});
