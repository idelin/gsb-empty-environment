import { deepEqual, getPath, isJSONObject, parsePath, type JSONValue } from './json.ts';
import { PlanError } from './errors.ts';
import { evalExpr } from './expr.ts';
import type { Cond, Step } from './plan.ts';

export function evalCond(cond: Cond, obj: JSONValue, stepId: string): boolean {
  switch (cond.kind) {
    case 'has':
      return getPath(obj, parsePath(cond.path)).found;
    case 'eq': {
      const r = getPath(obj, parsePath(cond.path));
      return r.found && deepEqual(r.value!, cond.value);
    }
    case 'not':
      return !evalCond(cond.cond, obj, stepId);
    case 'and':
      return cond.conds.every((c) => evalCond(c, obj, stepId));
    case 'or':
      return cond.conds.some((c) => evalCond(c, obj, stepId));
  }
}

function requireObject(v: JSONValue, stepId: string): { [key: string]: JSONValue } {
  if (!isJSONObject(v)) throw new PlanError(`步骤输入不是对象`, stepId, '$');
  return v;
}

/** 执行单个步骤，返回新对象。 */
export function runStep(step: Step, input: JSONValue): JSONValue {
  switch (step.op) {
    case 'project': {
      const obj = requireObject(input, step.id);
      const out: { [key: string]: JSONValue } = {};
      for (const f of step.fields) if (f in obj) out[f] = obj[f]!;
      return out;
    }
    case 'rename': {
      const obj = requireObject(input, step.id);
      if (!(step.from in obj)) {
        throw new PlanError(`运行时缺失字段 $.${step.from}`, step.id, `$.${step.from}`);
      }
      const out = { ...obj };
      out[step.to] = out[step.from]!;
      delete out[step.from];
      return out;
    }
    case 'compute': {
      const obj = requireObject(input, step.id);
      return { ...obj, [step.target]: evalExpr(step.expr, obj, step.id) };
    }
    case 'readDynamic': {
      const obj = requireObject(input, step.id);
      const v = evalExpr({ op: 'readDynamic', key: step.key }, obj, step.id);
      return { ...obj, [step.target]: v };
    }
    case 'delete': {
      const obj = requireObject(input, step.id);
      const out = { ...obj };
      delete out[step.field];
      return out;
    }
    case 'branch': {
      const obj = requireObject(input, step.id);
      const taken = evalCond(step.cond, obj, step.id) ? step.then : step.else;
      return runSteps(taken, obj);
    }
  }
}

export function runSteps(steps: Step[], input: JSONValue): JSONValue {
  let cur = input;
  for (const s of steps) cur = runStep(s, cur);
  return cur;
}

/** 运行整个计划；缺失字段等错误以 PlanError 抛出（含步骤与路径）。 */
export function runPlan(steps: Step[], input: JSONValue): JSONValue {
  return runSteps(steps, input);
}

/** 逐步运行并记录每个顶层步骤后的快照（供枚举检查器核对静态声称）。 */
export function runPlanTraced(steps: Step[], input: JSONValue): JSONValue[] {
  const snapshots: JSONValue[] = [];
  let cur = input;
  for (const s of steps) {
    cur = runStep(s, cur);
    snapshots.push(cur);
  }
  return snapshots;
}
