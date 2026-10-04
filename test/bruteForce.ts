import { createModel } from '../src/model';
import { optimalPlan } from '../src/planner';
import { Action, PlannerInput, ResourceVector } from '../src/types';

// 随机小实例上与穷举搜索对拍，验证规划器最优性（收益最大、成本最小）。

interface ProfitCost {
  profit: number;
  cost: number;
}

function mulberry32(seed: number) {
  let state = seed >>> 0;
  return () => {
    state |= 0;
    state = (state + 0x6D2B79F5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomInput(rand: () => number): PlannerInput {
  const ri = (n: number): number => Math.floor(rand() * n);
  const resNames = ['r0', 'r1', 'r2'].slice(0, 2 + ri(2));
  const initialResources: ResourceVector = {};
  for (const res of resNames) initialResources[res] = 3 + ri(6);
  const states = ['s0', 's1', 's2', 's3'].slice(0, 2 + ri(3));
  const nGoals = 2 + ri(3);
  const goals: { id: string; profit: number }[] = [];
  for (let gi = 0; gi < nGoals; gi++) goals.push({ id: 'g' + gi, profit: 1 + ri(15) });
  const actions: Action[] = [];
  const nActions = 4 + ri(6);
  for (let ai = 0; ai < nActions; ai++) {
    const consumes: ResourceVector = {};
    for (const res of resNames) {
      const amount = ri(4);
      if (amount > 0) consumes[res] = amount;
    }
    const from = states.filter(() => rand() < 0.5);
    if (from.length === 0) from.push(states[ri(states.length)]);
    const achieves = goals.filter(() => rand() < 0.3).map(goal => goal.id);
    const requiresGoals = goals.filter(() => rand() < 0.15).map(goal => goal.id);
    actions.push({
      name: 'a' + ai,
      cost: ri(5),
      consumes,
      from,
      to: states[ri(states.length)],
      achieves,
      requiresGoals,
    });
  }
  return { initialState: 's0', initialResources, goals, actions, maxSteps: 10, nearDistance: 2 };
}

interface CostDepth {
  cost: number;
  depth: number;
}

function bruteForce(input: PlannerInput): ProfitCost {
  const model = createModel(input);
  const allProfit = input.goals.reduce((sum, goal) => sum + goal.profit, 0);
  const maxSteps = input.maxSteps ?? 64;
  let bestProfit = 0;
  let bestCost = 0;
  const seen: Map<string, CostDepth[]> = new Map();

  function dfs(state: string, mask: bigint, remaining: ResourceVector, profit: number, cost: number, depth: number): void {
    if (profit > bestProfit || (profit === bestProfit && cost < bestCost)) {
      bestProfit = profit;
      bestCost = cost;
    }
    if (depth >= maxSteps) return;
    if (profit + (allProfit - model.profitOfMask(mask)) < bestProfit) return;
    const key = state + '|' + mask.toString(36) + '|' + model.resources.map(res => remaining[res]).join(',');
    const visits = seen.get(key);
    if (visits !== undefined && visits.some(visit => visit.cost <= cost && visit.depth <= depth)) return;
    const entry: CostDepth = { cost, depth };
    if (visits === undefined) seen.set(key, [entry]);
    else visits.push(entry);
    for (const action of model.actions) {
      if (!model.applicable(action, state, mask)) continue;
      if (!model.affordable(action, remaining)) continue;
      const next = model.apply(action, mask, remaining);
      dfs(next.state, next.mask, next.remaining, profit + model.profitOfMask(next.gained), cost + action.cost, depth + 1);
    }
  }

  dfs(model.initialState, 0n, { ...model.initialResources }, 0, 0, 0);
  return { profit: bestProfit, cost: bestCost };
}

const TRIALS = 1000;
const rand = mulberry32(20261005);
for (let trial = 0; trial < TRIALS; trial++) {
  const input = randomInput(rand);
  const expected = bruteForce(input);
  const actual = optimalPlan(input);
  if (actual.profit !== expected.profit || actual.cost !== expected.cost) {
    console.error('第 ' + trial + ' 例不一致: 规划器=' + actual.profit + '/' + actual.cost + ' 暴力=' + expected.profit + '/' + expected.cost);
    console.error(JSON.stringify(input));
    process.exit(1);
  }
}
console.log('暴力对拍通过: ' + TRIALS + ' 个随机实例的最优收益/成本全部一致 ✓');
