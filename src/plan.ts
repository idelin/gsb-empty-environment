import type { Expr } from './expr.ts';
import type { JSONValue } from './json.ts';

/** 计划步骤：投影 / 改名 / 计算 / 删除 / 分支 / 动态读取。 */
export type Step =
  | { id: string; op: 'project'; fields: string[] }
  | { id: string; op: 'rename'; from: string; to: string }
  | { id: string; op: 'compute'; target: string; expr: Expr }
  | { id: string; op: 'delete'; field: string }
  | { id: string; op: 'branch'; cond: Cond; then: Step[]; else: Step[] }
  | { id: string; op: 'readDynamic'; target: string; key: Expr };

/** 分支条件：支持路径敏感的 has / eq / not / and / or。 */
export type Cond =
  | { kind: 'has'; path: string }
  | { kind: 'eq'; path: string; value: JSONValue }
  | { kind: 'not'; cond: Cond }
  | { kind: 'and'; conds: Cond[] }
  | { kind: 'or'; conds: Cond[] };

/** 拍平（含分支子步骤），用于校验 id 唯一性。 */
export function collectSteps(steps: Step[]): Step[] {
  const out: Step[] = [];
  for (const s of steps) {
    out.push(s);
    if (s.op === 'branch') out.push(...collectSteps(s.then), ...collectSteps(s.else));
  }
  return out;
}

/** 步骤直接写入的顶层字段路径（分支递归聚合）。 */
export function writesOf(step: Step): string[] {
  switch (step.op) {
    case 'project':
      return step.fields.map((f) => '$.' + f);
    case 'rename':
      return ['$.' + step.from, '$.' + step.to];
    case 'compute':
    case 'readDynamic':
      return ['$.' + step.target];
    case 'delete':
      return ['$.' + step.field];
    case 'branch':
      return [...step.then, ...step.else].flatMap(writesOf);
  }
}
