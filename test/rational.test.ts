import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { isqrt, Rational } from '../src/rational.ts';

describe('Rational.parse：精确解析，不经 Number', () => {
  it('解析整数、小数、科学计数法', () => {
    assert.equal(Rational.parse('42').toString(), '42');
    assert.equal(Rational.parse('0.5').toString(), '1/2');
    assert.equal(Rational.parse('1.50').toString(), '3/2'); // 自动约分
    assert.equal(Rational.parse('-0.125').toString(), '-1/8');
    assert.equal(Rational.parse('1e3').toString(), '1000');
    assert.equal(Rational.parse('2.5e-1').toString(), '1/4');
    assert.equal(Rational.parse('.5').toString(), '1/2');
    assert.equal(Rational.parse('-0').toString(), '0'); // 无负零
  });

  it('超出 Number 安全范围的大数保持精确', () => {
    const big = '9007199254740993'; // 2^53 + 1，Number 无法精确表示
    assert.equal(Rational.parse(big).toString(), big);
    const huge = '123456789012345678901234567890.5';
    assert.equal(Rational.parse(huge).toString(), '246913578024691357802469135781/2');
  });

  it('拒绝非法字面量', () => {
    for (const bad of ['', '.', '+', 'abc', '1.2.3', '1e', '0x10']) {
      assert.throws(() => Rational.parse(bad), SyntaxError);
    }
  });

  it('number 输入仅接受安全整数', () => {
    assert.equal(Rational.from(7).toString(), '7');
    assert.throws(() => Rational.from(2 ** 53), TypeError);
    assert.throws(() => Rational.from(0.1), TypeError); // 浮点会失真，拒绝
  });
});

describe('Rational 四则运算（精确）', () => {
  it('加减乘除与约分', () => {
    const third = Rational.parse('1').div(3n);
    assert.equal(third.add(Rational.parse('1').div(6n)).toString(), '1/2');
    assert.equal(third.mul(6n).toString(), '2');
    assert.equal(Rational.parse('-1').div(-2n).toString(), '1/2'); // 符号归一
    assert.equal(third.sub(third).toString(), '0');
  });

  it('0.1 + 0.2 精确等于 0.3（无浮点误差）', () => {
    const s = Rational.parse('0.1').add(Rational.parse('0.2'));
    assert.equal(s.toString(), '3/10');
    assert.equal(s.toDecimalString(1), '0.3');
  });

  it('除以零抛错', () => {
    assert.throws(() => Rational.one.div(0n), RangeError);
  });
});

describe('toDecimalString：显式舍入规则', () => {
  it('平局时各模式行为不同', () => {
    const twoPointFive = Rational.parse('2.5');
    assert.equal(twoPointFive.toDecimalString(0, 'half-even'), '2'); // 向偶数
    assert.equal(twoPointFive.toDecimalString(0, 'half-up'), '3');
    assert.equal(twoPointFive.toDecimalString(0, 'half-down'), '2');
    assert.equal(Rational.parse('3.5').toDecimalString(0, 'half-even'), '4');
  });

  it('负数平局', () => {
    const neg = Rational.parse('-2.5');
    assert.equal(neg.toDecimalString(0, 'half-even'), '-2');
    assert.equal(neg.toDecimalString(0, 'half-up'), '-3'); // 远离零
    assert.equal(neg.toDecimalString(0, 'floor'), '-3');
    assert.equal(neg.toDecimalString(0, 'ceil'), '-2');
    assert.equal(neg.toDecimalString(0, 'down'), '-2'); // 向零截断
    assert.equal(neg.toDecimalString(0, 'up'), '-3');
  });

  it('1.005 保留两位：half-up 进位，half-even 不进', () => {
    const v = Rational.parse('1.005');
    assert.equal(v.toDecimalString(2, 'half-up'), '1.01');
    assert.equal(v.toDecimalString(2, 'half-even'), '1.00');
    assert.equal(v.toDecimalString(2, 'down'), '1.00');
    assert.equal(v.toDecimalString(2, 'up'), '1.01');
  });

  it('截断与进位（非平局）', () => {
    const v = Rational.parse('1.009');
    assert.equal(v.toDecimalString(2, 'down'), '1.00');
    assert.equal(v.toDecimalString(2, 'up'), '1.01');
    assert.equal(Rational.parse('-1.009').toDecimalString(2, 'floor'), '-1.01');
    assert.equal(Rational.parse('-1.009').toDecimalString(2, 'ceil'), '-1.00');
  });

  it('舍入到 0 时不输出负号', () => {
    assert.equal(Rational.parse('-0.001').toDecimalString(2, 'down'), '0.00');
  });

  it('大数格式化不经 Number', () => {
    const v = Rational.parse('123456789012345678901234567890.555');
    assert.equal(v.toDecimalString(2, 'half-up'), '123456789012345678901234567890.56');
    assert.equal(v.toDecimalString(2, 'down'), '123456789012345678901234567890.55');
  });
});

describe('isqrt', () => {
  it('精确平方与向下取整', () => {
    assert.equal(isqrt(0n), 0n);
    assert.equal(isqrt(1n), 1n);
    assert.equal(isqrt(99n), 9n);
    assert.equal(isqrt(100n), 10n);
    const big = 10n ** 40n;
    assert.equal(isqrt(big * big + 1n), big);
    assert.equal(isqrt(big * big - 1n), big - 1n);
  });
});
