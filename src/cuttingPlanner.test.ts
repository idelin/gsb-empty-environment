import assert from 'node:assert/strict';
import {
  solveCuttingProblem,
  MAX_TOTAL_PIECES,
  type Problem,
} from './cuttingPlanner.ts';

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

test('锯缝从可用长度扣除，不计入余料', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [{ length: 1000, price: 10 }],
    demands: [{ length: 600, quantity: 1 }],
  });
  assert.equal(plan.totalCost, 10);
  assert.equal(plan.totalCuts, 1);
  assert.equal(plan.bars.length, 1);
  // 余料 = 1000 - 600 - 10（锯缝），不是 400
  assert.equal(plan.bars[0].leftover, 390);
  assert.equal(plan.bars[0].leftoverUse, 'scrap'); // 390 < 最短订单 600
});

test('锯缝占用导致一根放不下两件', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [{ length: 1000, price: 10 }],
    demands: [{ length: 500, quantity: 2 }],
  });
  // 500+10+500 = 1010 > 1000，必须两根
  assert.equal(plan.bars.length, 2);
  assert.equal(plan.totalCost, 20);
  assert.equal(plan.totalCuts, 2);
  assert.equal(plan.bars[0].leftover, 490);
});

test('恰好用尽时最后一件免切', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [{ length: 1810, price: 10 }],
    demands: [{ length: 900, quantity: 2 }],
  });
  // 900 + 10 + 900 = 1810 恰好用尽，只切 1 刀
  assert.equal(plan.bars.length, 1);
  assert.equal(plan.totalCuts, 1);
  assert.equal(plan.bars[0].leftover, 0);
  assert.equal(plan.bars[0].leftoverUse, 'none');
});

test('成本相同取切割次数更少者', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [
      { length: 1000, price: 5 },
      { length: 1810, price: 10 },
    ],
    demands: [{ length: 900, quantity: 2 }],
  });
  // 两根 1000：成本 10、切 2 刀；一根 1810：成本 10、切 1 刀
  assert.equal(plan.totalCost, 10);
  assert.equal(plan.totalCuts, 1);
  assert.equal(plan.bars[0].stockLength, 1810);
});

test('成本优先级高于切割次数', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [
      { length: 1000, price: 4 },
      { length: 1810, price: 10 },
    ],
    demands: [{ length: 900, quantity: 2 }],
  });
  // 两根 1000：成本 8、切 2 刀，优于一根 1810：成本 10、切 1 刀
  assert.equal(plan.totalCost, 8);
  assert.equal(plan.totalCuts, 2);
  assert.equal(plan.bars.length, 2);
});

test('余料继续用于后续订单件', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [
      { length: 2000, price: 30 },
      { length: 1000, price: 20 },
    ],
    demands: [
      { length: 1500, quantity: 1 },
      { length: 400, quantity: 1 },
    ],
  });
  // 一根 2000：1500 + 10 + 400 + 10 = 1920，余 80
  assert.equal(plan.totalCost, 30);
  assert.equal(plan.bars.length, 1);
  assert.equal(plan.totalCuts, 2);
  assert.equal(plan.bars[0].leftover, 80);
  assert.equal(plan.bars[0].leftoverUse, 'scrap'); // 80 < 最短订单 400
});

test('够长的余料标记为可复用', () => {
  const plan = solveCuttingProblem({
    kerf: 10,
    stocks: [{ length: 2000, price: 30 }],
    demands: [{ length: 600, quantity: 1 }],
  });
  assert.equal(plan.bars[0].leftover, 1390);
  assert.equal(plan.bars[0].leftoverUse, 'reusable');
});

test('自定义复用门槛', () => {
  const problem: Problem = {
    kerf: 10,
    stocks: [{ length: 2000, price: 30 }],
    demands: [{ length: 600, quantity: 1 }],
    reusableMinLength: 1500,
  };
  const plan = solveCuttingProblem(problem);
  assert.equal(plan.bars[0].leftover, 1390);
  assert.equal(plan.bars[0].leftoverUse, 'scrap'); // 1390 < 1500
});

test('混合规格选最省钱组合', () => {
  const plan = solveCuttingProblem({
    kerf: 3,
    stocks: [
      { length: 6000, price: 50 },
      { length: 4000, price: 36 },
    ],
    demands: [
      { length: 2100, quantity: 2 },
      { length: 1500, quantity: 2 },
    ],
  });
  // 最优：两根 4000，各切 2100+3+1500+3 = 3606，余 394，各 2 刀
  // 总成本 72，总切 4 刀（优于 6000+4000 组合的 86）
  assert.equal(plan.totalCost, 72);
  assert.equal(plan.totalCuts, 4);
  assert.equal(plan.bars.length, 2);
  assert.ok(plan.bars.every((b) => b.stockLength === 4000));
  assert.ok(plan.bars.every((b) => b.leftover === 394));
});

test('订单件超过最长原料棒时报无解', () => {
  assert.throws(
    () =>
      solveCuttingProblem({
        kerf: 10,
        stocks: [{ length: 1000, price: 10 }],
        demands: [{ length: 3000, quantity: 1 }],
      }),
    /无解/,
  );
});

test('需求总件数超过上限时报错', () => {
  assert.throws(
    () =>
      solveCuttingProblem({
        kerf: 1,
        stocks: [{ length: 100, price: 1 }],
        demands: [{ length: 10, quantity: MAX_TOTAL_PIECES + 1 }],
      }),
    /超过上限/,
  );
});

test('需求总件数等于上限时可求解', () => {
  const plan = solveCuttingProblem({
    kerf: 1,
    stocks: [{ length: 100, price: 1 }],
    demands: [{ length: 10, quantity: MAX_TOTAL_PIECES }],
  });
  assert.equal(plan.totalPieces, MAX_TOTAL_PIECES);
  const produced = plan.bars.reduce((n, b) => n + b.pieces.length, 0);
  assert.equal(produced, MAX_TOTAL_PIECES);
});

console.log(`\n${passed} 个用例通过`);
