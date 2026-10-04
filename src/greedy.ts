import { createModel } from './model';
import { PlanStep, PlanSummary, PlannerInput, ResourceVector } from './types';

// 距离贪心基线（反面对照）：每步锁定图距离最近的未达成目标，
// 在可负担的动作中选使该目标距离下降最多者。不考虑全局资源预算，
// 因此会走进“看似接近目标却耗尽关键资源”的死端。
export function greedyPlan(input: PlannerInput): PlanSummary {
  const model = createModel(input);
  let state = model.initialState;
  let mask = 0n;
  let remaining: ResourceVector = { ...model.initialResources };
  let profit = 0;
  let cost = 0;
  const steps: PlanStep[] = [];

  while (steps.length < model.maxSteps) {
    let target = -1;
    let targetDist = Infinity;
    for (let goal = 0; goal < model.goalIds.length; goal++) {
      if ((mask & model.bit(goal)) !== 0n) continue;
      const dist = model.distanceToGoal(state, mask, goal);
      if (dist !== null && dist < targetDist) {
        target = goal;
        targetDist = dist;
      }
    }
    if (target === -1) break;

    let pick = -1;
    let pickDist = targetDist;
    let pickCost = Infinity;
    for (let actIdx = 0; actIdx < model.actions.length; actIdx++) {
      const action = model.actions[actIdx];
      if (!model.applicable(action, state, mask)) continue;
      if (!model.affordable(action, remaining)) continue;
      const next = model.apply(action, mask, remaining);
      const dist = model.distanceToGoal(next.state, next.mask, target);
      if (dist === null) continue;
      if (dist < pickDist || (dist === pickDist && action.cost < pickCost)) {
        pick = actIdx;
        pickDist = dist;
        pickCost = action.cost;
      }
    }
    if (pick === -1) break; // 没有能缩短距离的可行动作：贪心被困

    const action = model.actions[pick];
    const next = model.apply(action, mask, remaining);
    profit += model.profitOfMask(next.gained);
    cost += action.cost;
    state = next.state;
    mask = next.mask;
    remaining = next.remaining;
    steps.push({
      action: action.name,
      cost: action.cost,
      consumes: { ...action.consumes },
      balances: { ...remaining },
      state,
      achievedGoals: model.goalsOfMask(next.gained),
    });
  }

  return {
    profit,
    cost,
    steps,
    achievedGoals: model.goalsOfMask(mask),
    finalState: state,
    finalBalances: remaining,
  };
}
