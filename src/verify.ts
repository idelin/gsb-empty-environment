import { normalize } from './internal';
import { Assignment, Problem, Verification } from './types';

/** 独立校验一份分配是否满足全部约束（专业、回避、人数、评委对联合限制）。 */
export function verifyAssignment(problem: Problem, assignment: Assignment): Verification {
  const norm = normalize(problem);
  const { K, P, workIds, judgeIds } = norm;
  const violations: string[] = [];
  const loads: Record<string, number> = {};
  for (const id of judgeIds) loads[id] = 0;
  const pairCoJudging: Record<string, number> = {};
  const jIndex = new Map(judgeIds.map((id, i) => [id, i] as const));

  workIds.forEach((wid, wi) => {
    const js = assignment[wid];
    if (!js) {
      violations.push(`作品 ${wid} 未分配评委`);
      return;
    }
    if (js.length !== K) violations.push(`作品 ${wid} 评委数为 ${js.length}，应为 ${K}`);
    if (new Set(js).size !== js.length) violations.push(`作品 ${wid} 的评委存在重复`);
    for (const jid of js) {
      const ji = jIndex.get(jid);
      if (ji === undefined) {
        violations.push(`作品 ${wid} 分配了未知评委 ${jid}`);
        continue;
      }
      loads[jid]++;
      if (!norm.judgeFields[ji].includes(norm.workFields[wi])) {
        violations.push(`评委 ${jid} 的专业与作品 ${wid}（${norm.workFields[wi]}）不匹配`);
      }
      if (norm.conflicted[ji][wi]) violations.push(`评委 ${jid} 与作品 ${wid} 存在回避关系`);
    }
    for (let a = 0; a < js.length; a++) {
      for (let b = a + 1; b < js.length; b++) {
        const key = [js[a], js[b]].sort().join('|');
        pairCoJudging[key] = (pairCoJudging[key] ?? 0) + 1;
      }
    }
  });
  for (const wid of Object.keys(assignment)) {
    if (!workIds.includes(wid)) violations.push(`分配中包含未知作品 ${wid}`);
  }
  for (const [key, c] of Object.entries(pairCoJudging)) {
    if (c > P) violations.push(`评委对 ${key} 共同评审 ${c} 份作品，超过上限 ${P}`);
  }

  const vals = Object.values(loads);
  return {
    valid: violations.length === 0,
    violations,
    loads,
    maxLoadDiff: vals.length ? Math.max(...vals) - Math.min(...vals) : 0,
    pairCoJudging,
  };
}
