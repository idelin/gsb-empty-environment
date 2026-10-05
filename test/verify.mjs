// 不变量校验与边界用例测试：node test/verify.mjs（需先编译出 dist/）
import assert from 'node:assert';
import { solve } from '../dist/solver.js';
import { formatSolution } from '../dist/report.js';
import fs from 'node:fs';

let passed = 0;
const ok = (name) => {
  passed++;
  console.log(`  ok - ${name}`);
};

/** 校验一条方案的所有硬约束 */
function checkInvariants(name, problem, sol) {
  const n = problem.stations.length;
  const cap = problem.vehicleCapacity;
  let inv = problem.stations.map((s) => s.initial);
  let load = 0;
  let pos = -1;
  let km = 0;
  const total0 = inv.reduce((a, b) => a + b, 0);

  assert(sol.steps.length === 0 || sol.steps[0].from === -1, `${name}: 必须从仓库出发`);
  for (const [idx, step] of sol.steps.entries()) {
    assert.strictEqual(step.from, pos, `${name}: 步骤 ${idx + 1} 起点不连续`);
    assert.notStrictEqual(step.to, pos, `${name}: 步骤 ${idx + 1} 不能原地移动`);
    const xs = [problem.depot.x, ...problem.stations.map((s) => s.x)];
    const ys = [problem.depot.y, ...problem.stations.map((s) => s.y)];
    const expectKm = Math.hypot(xs[pos + 1] - xs[step.to + 1], ys[pos + 1] - ys[step.to + 1]);
    assert(Math.abs(step.driveKm - expectKm) < 1e-9, `${name}: 步骤 ${idx + 1} 里程错误`);
    km += step.driveKm;
    if (step.to === -1) {
      assert.strictEqual(step.delta, 0, `${name}: 仓库不允许装卸`);
    } else {
      inv[step.to] += step.delta;
      load -= step.delta;
      assert(inv[step.to] >= 0, `${name}: 步骤 ${idx + 1} 站点 ${step.to} 出现负库存`);
      assert(
        inv[step.to] <= problem.stations[step.to].capacity,
        `${name}: 步骤 ${idx + 1} 站点 ${step.to} 超过容量`,
      );
    }
    assert(load >= 0 && load <= cap, `${name}: 步骤 ${idx + 1} 车载越界 (${load})`);
    assert.deepStrictEqual(step.invAfter, inv, `${name}: 步骤 ${idx + 1} 库存快照不一致`);
    assert.strictEqual(step.loadAfter, load, `${name}: 步骤 ${idx + 1} 车载快照不一致`);
    assert(
      inv.reduce((a, b) => a + b, 0) + load === total0,
      `${name}: 步骤 ${idx + 1} 车辆总数不守恒`,
    );
    pos = step.to;
  }
  assert.strictEqual(pos, -1, `${name}: 必须回仓结束`);
  // 成本核算
  const shortage = inv.map((v, i) =>
    Math.max(0, Math.min(problem.stations[i].demand, problem.stations[i].capacity) - v),
  );
  assert.deepStrictEqual(sol.shortage, shortage, `${name}: 缺车数错误`);
  assert(Math.abs(sol.totalKm - km) < 1e-9, `${name}: 总里程错误`);
  assert(Math.abs(sol.mileageCost - km * problem.costPerKm) < 1e-9, `${name}: 里程成本错误`);
  const penalty = shortage.reduce((a, b) => a + b, 0) * problem.penaltyPerBike;
  assert(Math.abs(sol.penalty - penalty) < 1e-9, `${name}: 罚金错误`);
  assert(Math.abs(sol.totalCost - (sol.mileageCost + sol.penalty)) < 1e-9, `${name}: 总成本错误`);
  ok(name);
}

const sample = JSON.parse(fs.readFileSync(new URL('../examples/sample.json', import.meta.url)));
checkInvariants('示例问题（4 站）', sample, solve(sample));

// 1. 初始即满足需求：空路线、零成本
{
  const p = {
    depot: { x: 0, y: 0 },
    vehicleCapacity: 5,
    costPerKm: 1,
    penaltyPerBike: 10,
    stations: [{ id: 'A', x: 1, y: 0, capacity: 10, initial: 5, demand: 5 }],
  };
  const sol = solve(p);
  checkInvariants('初始已满足需求', p, sol);
  assert.strictEqual(sol.totalCost, 0);
  assert.strictEqual(sol.steps.length, 0);
  ok('零成本空路线');
}

// 2. 负库存约束：缺车站旁边没有富余站时，不能凭空取车
{
  const p = {
    depot: { x: 0, y: 0 },
    vehicleCapacity: 10,
    costPerKm: 1,
    penaltyPerBike: 100,
    stations: [
      { id: 'S', x: 5, y: 0, capacity: 10, initial: 0, demand: 6 },
      { id: 'T', x: -5, y: 0, capacity: 10, initial: 0, demand: 0 },
    ],
  };
  const sol = solve(p);
  checkInvariants('无车可取时只能认罚', p, sol);
  assert.strictEqual(sol.shortage[0], 6);
  assert.strictEqual(sol.penalty, 600);
  ok('负库存被禁止');
}

// 3. 需求超过容量：目标应被钳制到容量
{
  const p = {
    depot: { x: 0, y: 0 },
    vehicleCapacity: 20,
    costPerKm: 1,
    penaltyPerBike: 50,
    stations: [
      { id: 'A', x: 3, y: 0, capacity: 5, initial: 0, demand: 9 },
      { id: 'B', x: -3, y: 0, capacity: 10, initial: 9, demand: 0 },
    ],
  };
  const sol = solve(p);
  checkInvariants('需求超过容量时按容量计', p, sol);
  assert.strictEqual(sol.shortage[0], 0, '目标钳制为容量 5 后应可满足');
  ok('需求钳制到容量');
}

// 4. 载重上限迫使多次往返
{
  const p = {
    depot: { x: 0, y: 0 },
    vehicleCapacity: 4,
    costPerKm: 1,
    penaltyPerBike: 1000,
    stations: [
      { id: 'A', x: 1, y: 0, capacity: 20, initial: 12, demand: 0 },
      { id: 'B', x: 2, y: 0, capacity: 20, initial: 0, demand: 8 },
    ],
  };
  const sol = solve(p);
  checkInvariants('载重限制下多次往返', p, sol);
  assert.strictEqual(sol.penalty, 0, '罚金足够高时应全部送达');
  const delivered = sol.steps.filter((s) => s.delta > 0).reduce((a, s) => a + s.delta, 0);
  assert(delivered >= 8);
  ok('多次访问/往返可行');
}

// 5. 罚金太低时：出车不划算，应直接认罚
{
  const p = {
    depot: { x: 0, y: 0 },
    vehicleCapacity: 10,
    costPerKm: 1,
    penaltyPerBike: 1,
    stations: [
      { id: 'A', x: 100, y: 0, capacity: 10, initial: 0, demand: 3 },
      { id: 'B', x: -100, y: 0, capacity: 10, initial: 5, demand: 0 },
    ],
  };
  const sol = solve(p);
  checkInvariants('罚金低于运费时认罚', p, sol);
  assert.strictEqual(sol.totalCost, 3);
  assert.strictEqual(sol.steps.length, 0);
  ok('经济性取舍正确');
}

// 6. 八站上限与非法输入
{
  const mk = (n) => ({
    depot: { x: 0, y: 0 },
    vehicleCapacity: 5,
    costPerKm: 1,
    penaltyPerBike: 1,
    stations: Array.from({ length: n }, (_, i) => ({
      id: `S${i}`,
      x: i + 1,
      y: 0,
      capacity: 5,
      initial: 2,
      demand: 2,
    })),
  });
  checkInvariants('八站可求解', mk(8), solve(mk(8)));
  assert.throws(() => solve(mk(9)), /最多 8/);
  const bad = mk(2);
  bad.stations[0].initial = 99;
  assert.throws(() => solve(bad), /超过容量/);
  ok('输入校验生效');
}

// 7. 时间线输出可渲染
{
  const text = formatSolution(sample, solve(sample));
  assert(text.includes('=== 调度方案 ==='));
  assert(text.includes('车辆已回仓'));
  ok('时间线渲染正常');
}

console.log(`\n全部通过：${passed} 项检查`);
