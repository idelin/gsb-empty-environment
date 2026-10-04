import { describe, expect, test } from 'bun:test';
import {
  ProviderContractError,
  Rational,
  SimulatedProvider,
  TopKSorter,
  interval,
} from '../src/index';

const r = (n: number, d = 1) => Rational.of(n, d);

describe('Rational', () => {
  test('规范化与精确比较', () => {
    expect(r(2, 4).toString()).toBe('1/2');
    expect(r(1, 3).cmp(r(2, 6))).toBe(0);
    expect(r(1, 3).cmp(r(1, 2))).toBe(-1);
    expect(r(-3, -4).toString()).toBe('3/4');
  });
});

describe('TopKSorter 顺序判定', () => {
  test('重叠区间收紧至不相交后，给出完整确定顺序', () => {
    // 真值 10/20/30，初始区间两两重叠，收紧一次后全部分离
    const a = SimulatedProvider.halving('a', 10, { radius: 8, intervalSteps: 3 });
    const b = SimulatedProvider.halving('b', 20, { radius: 8, intervalSteps: 3 });
    const c = SimulatedProvider.halving('c', 30, { radius: 8, intervalSteps: 3 });
    const sorter = new TopKSorter([a, b, c], { k: 3, budget: 10 });
    const res = sorter.run();
    expect(res.status).toBe('complete');
    expect(res.prefix).toEqual([['c'], ['b'], ['a']]);
    expect(res.pending).toEqual([]);
    expect(res.budgetUsed).toBe(3); // 每项各收紧一次即足够
  });

  test('精确证书与区间不相交同样证明顺序', () => {
    const a = new SimulatedProvider('a', [
      { kind: 'interval', interval: interval(r(4), r(12)) }, // 初始与 b 重叠
      { kind: 'exact', value: r(5) },
    ]);
    const b = new SimulatedProvider('b', [{ kind: 'interval', interval: interval(r(10), r(20)) }]);
    const sorter = new TopKSorter([a, b], { k: 2, budget: 10 });
    const res = sorter.run();
    expect(res.status).toBe('complete');
    expect(res.prefix).toEqual([['b'], ['a']]);
    expect(res.budgetUsed).toBe(2); // b 先被 refine（返回 exhausted），a 再给出精确证书
  });

  test('精确证书值相等 → 并列入前缀同一组', () => {
    const a = SimulatedProvider.halving('a', 10, { radius: 4, intervalSteps: 1, emitExact: true });
    const b = SimulatedProvider.halving('b', 10, { radius: 4, intervalSteps: 1, emitExact: true });
    const sorter = new TopKSorter([a, b], { k: 2, budget: 10 });
    const res = sorter.run();
    expect(res.status).toBe('complete');
    expect(res.prefix).toEqual([['a', 'b']]);
    expect(res.pending).toEqual([]);
  });

  test('端点相接（lo == 对方 hi）不算不相交，保持未决', () => {
    const a = new SimulatedProvider('a', [
      { kind: 'interval', interval: interval(r(9), r(11)) },
      { kind: 'exact', value: r(10) },
    ]);
    // b 的区间下界恰好等于 a 的精确值 10，且 b 已无能为力
    const b = new SimulatedProvider('b', [{ kind: 'interval', interval: interval(r(10), r(14)) }]);
    const sorter = new TopKSorter([a, b], { k: 2, budget: 5 });
    const res = sorter.run();
    expect(res.prefix).toEqual([]);
    expect(res.pending).toEqual([['a', 'b']]);
  });
});

describe('TopKSorter 预算与未决结果', () => {
  test('预算耗尽：返回确定前缀与未决组，绝不按区间中点猜顺序', () => {
    // 中点 10 < 12，若按中点猜会输出 [b, a]；区间始终重叠则必须保持未决
    const a = SimulatedProvider.halving('a', 10, { radius: 8, intervalSteps: 1 });
    const b = SimulatedProvider.halving('b', 12, { radius: 8, intervalSteps: 1 });
    const sorter = new TopKSorter([a, b], { k: 2, budget: 2 });
    const res = sorter.run();
    expect(res.status).toBe('budget-exhausted');
    expect(res.budgetUsed).toBe(2);
    expect(res.prefix).toEqual([]);
    expect(res.pending).toEqual([['a', 'b']]);
  });

  test('部分确定：前缀已证，余下重叠项归入未决组', () => {
    const top = SimulatedProvider.halving('top', 100, { radius: 2, intervalSteps: 1 });
    const x = SimulatedProvider.halving('x', 10, { radius: 8, intervalSteps: 1 });
    const y = SimulatedProvider.halving('y', 12, { radius: 8, intervalSteps: 1 });
    const sorter = new TopKSorter([top, x, y], { k: 3, budget: 3 });
    const res = sorter.run();
    expect(res.status).toBe('budget-exhausted');
    expect(res.prefix).toEqual([['top']]);
    expect(res.pending).toEqual([['x', 'y']]);
  });

  test('提供者耗尽仍重叠 → providers-exhausted', () => {
    const a = new SimulatedProvider('a', [
      { kind: 'interval', interval: interval(r(0), r(10)) },
      { kind: 'interval', interval: interval(r(2), r(8)) },
    ]);
    const b = new SimulatedProvider('b', [
      { kind: 'interval', interval: interval(r(1), r(9)) },
      { kind: 'interval', interval: interval(r(3), r(7)) },
    ]);
    const sorter = new TopKSorter([a, b], { k: 2, budget: 10 });
    const res = sorter.run();
    expect(res.status).toBe('providers-exhausted');
    expect(res.prefix).toEqual([]);
    expect(res.pending).toEqual([['a', 'b']]);
  });

  test('K 边界：前 K 一旦确定即停止，不为未决尾部浪费预算', () => {
    const a = SimulatedProvider.halving('a', 40, { radius: 6, intervalSteps: 2 });
    const b = SimulatedProvider.halving('b', 30, { radius: 6, intervalSteps: 2 });
    // c、d 初始区间后即耗尽，且彼此重叠
    const c = new SimulatedProvider('c', [{ kind: 'interval', interval: interval(r(14), r(26)) }]);
    const d = new SimulatedProvider('d', [{ kind: 'interval', interval: interval(r(4), r(16)) }]);
    const sorter = new TopKSorter([a, b, c, d], { k: 2, budget: 10 });
    const res = sorter.run();
    expect(res.status).toBe('complete');
    expect(res.prefix).toEqual([['a'], ['b']]);
    expect(res.pending).toEqual([['c', 'd']]);
    expect(res.budgetUsed).toBe(2);
    expect(c.refineCalls).toBe(1); // 只有初始区间，未被额外 refine
    expect(d.refineCalls).toBe(1);
  });
});

describe('TopKSorter 提供者契约校验', () => {
  const buddy = () =>
    new SimulatedProvider('buddy', [{ kind: 'interval', interval: interval(r(0), r(10)) }]);

  test('区间未单调收紧 → ProviderContractError(not-tightening)', () => {
    const bad = new SimulatedProvider('bad', [
      { kind: 'interval', interval: interval(r(0), r(10)) },
      { kind: 'interval', interval: interval(r(5), r(20)) }, // 扩宽，违反收紧
    ]);
    const sorter = new TopKSorter([bad, buddy()], { k: 2, budget: 5 });
    let err: ProviderContractError | null = null;
    try {
      sorter.run();
    } catch (e) {
      err = e as ProviderContractError;
    }
    expect(err).toBeInstanceOf(ProviderContractError);
    expect(err!.violation).toBe('not-tightening');
    expect(err!.providerId).toBe('bad');
  });

  test('精确证书落在当前区间外 → ProviderContractError(exact-outside-interval)', () => {
    const bad = new SimulatedProvider('bad', [
      { kind: 'interval', interval: interval(r(0), r(10)) },
      { kind: 'exact', value: r(99) },
    ]);
    const sorter = new TopKSorter([bad, buddy()], { k: 2, budget: 5 });
    let err: ProviderContractError | null = null;
    try {
      sorter.run();
    } catch (e) {
      err = e as ProviderContractError;
    }
    expect(err).toBeInstanceOf(ProviderContractError);
    expect(err!.violation).toBe('exact-outside-interval');
  });

  test('非法区间（lo > hi）→ ProviderContractError(invalid-interval)', () => {
    const bad = new SimulatedProvider('bad', [
      { kind: 'interval', interval: interval(r(10), r(0)) },
    ]);
    expect(() => new TopKSorter([bad, buddy()], { k: 2, budget: 5 })).toThrow(
      ProviderContractError,
    );
  });
});

describe('TopKSorter 取消', () => {
  test('取消后立即停止：状态 cancelled，不再调用任何 refine', () => {
    const providers = [
      SimulatedProvider.halving('a', 10, { radius: 8, intervalSteps: 10 }),
      SimulatedProvider.halving('b', 20, { radius: 8, intervalSteps: 10 }),
      SimulatedProvider.halving('c', 30, { radius: 8, intervalSteps: 10 }),
    ];
    const sorter = new TopKSorter(providers, {
      k: 3,
      budget: 100,
      onStep: ({ used }) => {
        if (used === 2) sorter.cancel();
      },
    });
    const res = sorter.run();
    expect(res.status).toBe('cancelled');
    expect(res.budgetUsed).toBe(2);
    for (const p of providers) expect(p.cancelled).toBe(true);
    // 3 次初始 + 2 次收紧，取消后没有任何额外 refine
    expect(providers.reduce((n, p) => n + p.refineCalls, 0)).toBe(5);
  });

  test('运行前取消：直接返回 cancelled，不消耗预算', () => {
    const a = SimulatedProvider.halving('a', 1, { radius: 1, intervalSteps: 2 });
    const b = SimulatedProvider.halving('b', 2, { radius: 1, intervalSteps: 2 });
    const sorter = new TopKSorter([a, b], { k: 2, budget: 10 });
    sorter.cancel();
    const res = sorter.run();
    expect(res.status).toBe('cancelled');
    expect(res.budgetUsed).toBe(0);
  });
});
