import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { WindowStats } from '../src/stats.ts';

describe('基本统计量（样本 / 总体区分）', () => {
  it('[1,2,3,4]：sum=10, mean=5/2, 样本方差=5/3, 总体方差=5/4', () => {
    const w = new WindowStats();
    for (const v of ['1', '2', '3', '4']) w.add(v);
    assert.equal(w.count, 4);
    assert.equal(w.sum().toString(), '10');
    assert.equal(w.mean().toString(), '5/2');
    assert.equal(w.sampleVariance()!.toString(), '5/3');
    assert.equal(w.populationVariance()!.toString(), '5/4');
    assert.equal(w.variance('sample')!.toDecimalString(4, 'half-even'), '1.6667');
    assert.equal(w.variance('population')!.toDecimalString(2), '1.25');
  });

  it('小数输入精确累计：0.1+0.2+0.3 = 3/5 的均值 1/5', () => {
    const w = new WindowStats();
    w.add('0.1').add('0.2').add('0.3');
    assert.equal(w.sum().toString(), '3/5');
    assert.equal(w.mean().toString(), '1/5');
    assert.equal(w.mean().toDecimalString(1), '0.2');
  });
});

describe('大偏移 + 小方差：无灾难消减', () => {
  // 浮点实现中，10^30 量级的 Σx² 与 (Σx)²/n 相减会完全吞掉小方差；
  // 这里全程 BigInt 精确有理数，结果必须精确。
  it('10^30, 10^30+1, 10^30+2：样本方差精确为 1', () => {
    const base = 10n ** 30n;
    const w = new WindowStats();
    w.add(base.toString()).add((base + 1n).toString()).add((base + 2n).toString());
    assert.equal(w.mean().toString(), (base + 1n).toString());
    assert.equal(w.sampleVariance()!.toString(), '1');
    assert.equal(w.populationVariance()!.toString(), '2/3');
  });

  it('大数附近相差 1 的两个样本：样本方差精确为 1/2', () => {
    const a = '123456789012345678901234567890';
    const b = '123456789012345678901234567891';
    const w = new WindowStats();
    w.add(a).add(b);
    assert.equal(w.sampleVariance()!.toString(), '1/2');
    assert.equal(w.sampleVariance()!.toDecimalString(1), '0.5');
    assert.equal(w.populationVariance()!.toString(), '1/4');
  });

  it('科学计数法大输入：1e30 与 1e30+1', () => {
    const w = new WindowStats();
    w.add('1e30').add('1000000000000000000000000000001');
    assert.equal(w.sampleVariance()!.toString(), '1/2');
  });
});

describe('重复值与窗口增删', () => {
  it('重复值按多重集合处理，删除只撤销一次出现', () => {
    const w = new WindowStats();
    w.add('5').add('5').add('5').add('7');
    assert.equal(w.count, 4);
    assert.equal(w.sum().toString(), '22');
    w.remove('5');
    assert.equal(w.count, 3);
    assert.equal(w.sum().toString(), '17');
    assert.equal(w.mean().toString(), '17/3');
  });

  it('删除不存在的值抛 RangeError', () => {
    const w = new WindowStats();
    w.add('1');
    assert.throws(() => w.remove('2'), RangeError);
    assert.throws(() => w.remove('1').remove('1'), RangeError); // 只能删一次
  });

  it('滑动窗口：出窗入窗后统计量正确', () => {
    const w = new WindowStats();
    const stream = ['10', '20', '30', '40', '50'];
    w.add(stream[0]).add(stream[1]).add(stream[2]); // 窗口 [10,20,30]
    assert.equal(w.mean().toString(), '20');
    w.remove(stream[0]).add(stream[3]); // [20,30,40]
    assert.equal(w.mean().toString(), '30');
    assert.equal(w.sampleVariance()!.toString(), '100');
    w.remove(stream[1]).add(stream[4]); // [30,40,50]
    assert.equal(w.mean().toString(), '40');
    assert.equal(w.populationVariance()!.toString(), '200/3');
  });

  it('撤回恢复原统计：增删往返后与检查点完全一致', () => {
    const w = new WindowStats();
    w.add('3').add('1').add('4').add('1').add('5');
    const checkpoint = {
      sum: w.sum(),
      mean: w.mean(),
      sample: w.sampleVariance()!,
      population: w.populationVariance()!,
    };
    // 加入一批再全部撤回（含重复值与大数）
    const extra = ['9', '9', '2.5', '100000000000000000000000000000', '-4'];
    for (const v of extra) w.add(v);
    for (const v of extra) w.remove(v);
    assert.equal(w.count, 5);
    assert.ok(w.sum().equals(checkpoint.sum));
    assert.ok(w.mean().equals(checkpoint.mean));
    assert.ok(w.sampleVariance()!.equals(checkpoint.sample));
    assert.ok(w.populationVariance()!.equals(checkpoint.population));
  });

  it('全部删除后回到空窗口状态', () => {
    const w = new WindowStats();
    w.add('7').add('7').add('-3');
    w.remove('7').remove('7').remove('-3');
    assert.equal(w.count, 0);
    assert.ok(w.sum().isZero());
    assert.equal(w.sampleVariance(), null);
    assert.equal(w.populationVariance(), null);
    assert.throws(() => w.mean(), RangeError);
  });
});

describe('边界：未定义统计量', () => {
  it('n=1 时样本方差未定义（null），总体方差为 0', () => {
    const w = new WindowStats();
    w.add('42');
    assert.equal(w.sampleVariance(), null);
    assert.equal(w.populationVariance()!.toString(), '0');
    assert.equal(w.standardDeviation('sample', 2), null);
    assert.equal(w.standardDeviation('population', 2), '0.00');
  });

  it('空窗口：mean 抛错，方差为 null，sum 为 0', () => {
    const w = new WindowStats();
    assert.ok(w.sum().isZero());
    assert.throws(() => w.mean(), RangeError);
    assert.equal(w.variance('sample'), null);
    assert.equal(w.variance('population'), null);
  });
});

describe('标准差与快照', () => {
  it('经典数据集 [2,4,4,4,5,5,7,9]', () => {
    const w = new WindowStats();
    for (const v of [2, 4, 4, 4, 5, 5, 7, 9]) w.add(v);
    assert.equal(w.populationVariance()!.toString(), '4');
    assert.equal(w.sampleVariance()!.toString(), '32/7');
    assert.equal(w.standardDeviation('population', 3), '2.000');
    // sqrt(32/7) = 2.1380899…
    assert.equal(w.standardDeviation('sample', 4, 'half-even'), '2.1381');
    assert.equal(w.standardDeviation('sample', 4, 'down'), '2.1380');
  });

  it('snapshot 汇总格式化', () => {
    const w = new WindowStats();
    w.add('1').add('2').add('4');
    const snap = w.snapshot(3, 'half-up');
    assert.deepEqual(snap, {
      count: 3,
      sum: '7.000',
      mean: '2.333',
      sampleVariance: '2.333',
      populationVariance: '1.556',
      sampleStddev: '1.528',
      populationStddev: '1.247',
    });
  });
});
