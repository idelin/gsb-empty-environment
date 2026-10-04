import { createHeap } from './heap';
import { createModel } from './model';
import { BlockedGoal, DeadEnd, PlanResult, PlanStep, PlannerInput, ResourceVector } from './types';

// 搜索标签：一个可达的 (状态, 目标集合, 剩余资源) 结点及其代价
interface Label {
  id: number;
  state: string;
  mask: bigint;
  remaining: ResourceVector;
  profit: number;
  cost: number;
  depth: number;
  parent: number;
  action: number;
  alive: boolean;
}

const DEAD_END_CAP = 20;

// 最优规划：收益最大为主目标，动作成本最小为次目标。
// 按 (收益降序, 成本升序) 最佳优先扩展，配合 Pareto 支配剪枝与收益上界剪枝，
// 不做任何“朝目标距离最近”的贪心选择。
export function optimalPlan(input: PlannerInput): PlanResult {
  const model = createModel(input);
  const labels: Label[] = [];
  const frontier: Map<string, Label[]> = new Map();
  const heap = createHeap((idA, idB) => {
    const la = labels[idA];
    const lb = labels[idB];
    if (la.profit !== lb.profit) return lb.profit - la.profit;
    if (la.cost !== lb.cost) return la.cost - lb.cost;
    return la.depth - lb.depth;
  });

  const root: Label = {
    id: 0,
    state: model.initialState,
    mask: 0n,
    remaining: { ...model.initialResources },
    profit: 0,
    cost: 0,
    depth: 0,
    parent: -1,
    action: -1,
    alive: true,
  };
  labels.push(root);
  frontier.set(root.state, [root]);
  heap.push(root.id);

  let best: Label = root;
  const deadEnds: DeadEnd[] = [];
  const deadEndSeen: Set<string> = new Set();
  let explored = 0;

  while (heap.size() > 0) {
    const id = heap.pop();
    if (id === undefined) break;
    const label = labels[id];
    if (!label.alive) continue;
    explored++;

    if (label.profit > best.profit || (label.profit === best.profit && label.cost < best.cost)) {
      best = label;
    }

    const ceiling = model.profitCeiling(label.profit, label.state, label.mask, label.remaining);
    if (ceiling === label.profit) {
      // 剩余资源内无法再达成任何目标：若仍有图上可达的目标，则为资源耗尽的死端
      recordDeadEnd(label);
      continue;
    }
    if (ceiling < best.profit || (ceiling === best.profit && label.cost >= best.cost)) continue;
    if (label.depth >= model.maxSteps) continue;

    for (let actIdx = 0; actIdx < model.actions.length; actIdx++) {
      const action = model.actions[actIdx];
      if (!model.applicable(action, label.state, label.mask)) continue;
      if (!model.affordable(action, label.remaining)) continue;
      const next = model.apply(action, label.mask, label.remaining);
      const child: Label = {
        id: labels.length,
        state: next.state,
        mask: next.mask,
        remaining: next.remaining,
        profit: label.profit + model.profitOfMask(next.gained),
        cost: label.cost + action.cost,
        depth: label.depth + 1,
        parent: label.id,
        action: actIdx,
        alive: true,
      };
      const siblings = frontier.get(child.state);
      if (siblings !== undefined && siblings.some(other => dominates(other, child))) continue;
      const kept: Label[] = [];
      if (siblings !== undefined) {
        for (const other of siblings) {
          if (dominates(child, other)) other.alive = false;
          else kept.push(other);
        }
      }
      kept.push(child);
      frontier.set(child.state, kept);
      labels.push(child);
      heap.push(child.id);
    }
  }

  return {
    profit: best.profit,
    cost: best.cost,
    steps: buildSteps(best),
    achievedGoals: model.goalsOfMask(best.mask),
    finalState: best.state,
    finalBalances: { ...best.remaining },
    deadEnds: rankDeadEnds(deadEnds),
    explored,
  };

  // 支配：同状态下，目标集合为超集、资源逐项不少、成本与深度均不超过
  function dominates(a: Label, b: Label): boolean {
    if ((b.mask & ~a.mask) !== 0n) return false;
    if (a.cost > b.cost) return false;
    if (a.depth > b.depth) return false;
    for (const res of model.resources) {
      if (a.remaining[res] < b.remaining[res]) return false;
    }
    return true;
  }

  function prefixOf(leaf: Label): string[] {
    const names: string[] = [];
    let cur = leaf;
    while (cur.parent !== -1) {
      names.push(model.actions[cur.action].name);
      cur = labels[cur.parent];
    }
    names.reverse();
    return names;
  }

  function buildSteps(leaf: Label): PlanStep[] {
    const chain: Label[] = [];
    let cur = leaf;
    while (cur.parent !== -1) {
      chain.push(cur);
      cur = labels[cur.parent];
    }
    chain.reverse();
    return chain.map(node => {
      const action = model.actions[node.action];
      const gained = node.mask & ~labels[node.parent].mask;
      return {
        action: action.name,
        cost: action.cost,
        consumes: { ...action.consumes },
        balances: { ...node.remaining },
        state: node.state,
        achievedGoals: model.goalsOfMask(gained),
      };
    });
  }

  // 死端判定：上界等于当前收益（无可行目标），但仍有目标在图上可达——
  // 即被关键资源卡死，而非自然终点。报告每个被卡目标的图距离与资源缺口下界。
  function recordDeadEnd(label: Label): void {
    const blocked: BlockedGoal[] = [];
    for (let goal = 0; goal < model.goalIds.length; goal++) {
      if ((label.mask & model.bit(goal)) !== 0n) continue;
      const need = model.minNeedVector(label.state, label.mask, goal);
      if (need === null) continue; // 图上本就不可达，不是资源问题
      const missing: ResourceVector = {};
      let starved = false;
      for (const res of Object.keys(need)) {
        const deficit = need[res] - label.remaining[res];
        if (deficit > 0) {
          missing[res] = deficit;
          starved = true;
        }
      }
      if (!starved) continue;
      const distance = model.distanceToGoal(label.state, label.mask, goal);
      blocked.push({
        goal: model.goalIds[goal],
        profit: model.profitOf(goal),
        distance: distance === null ? -1 : distance,
        missing,
      });
    }
    if (blocked.length === 0) return;
    const key = label.state + '|' + label.mask.toString(36) + '|' + model.resources.map(res => label.remaining[res]).join(',');
    if (deadEndSeen.has(key)) return;
    deadEndSeen.add(key);
    deadEnds.push({
      prefix: prefixOf(label),
      state: label.state,
      remaining: { ...label.remaining },
      profit: label.profit,
      cost: label.cost,
      blockedGoals: blocked,
    });
  }

  // 输出排序：越“看似接近”（图距离小）且被卡收益越大的死端越靠前
  function rankDeadEnds(ends: DeadEnd[]): DeadEnd[] {
    const nearScore = (entry: DeadEnd): number => {
      let bestDist = Infinity;
      for (const blk of entry.blockedGoals) {
        if (blk.distance !== -1 && blk.distance < bestDist) bestDist = blk.distance;
      }
      return bestDist;
    };
    const blockedProfit = (entry: DeadEnd): number => entry.blockedGoals.reduce((sum, blk) => sum + blk.profit, 0);
    return ends
      .slice()
      .sort((lhs, rhs) => nearScore(lhs) - nearScore(rhs) || blockedProfit(rhs) - blockedProfit(lhs) || lhs.cost - rhs.cost)
      .slice(0, DEAD_END_CAP);
  }
}
