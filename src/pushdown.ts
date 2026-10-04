// 谓词下推优化器。
//
// 合法规则（本优化器只应用这些）：
//   P1  σp(σq(R))            => σp∧q(R) 合并，继续下推
//   P2  σp(R ⋈ S)，p 只引用 R 的列，且连接为 inner  => σp(R) ⋈ S
//   P3  σp(R ⟕ S)，p 只引用左表列                   => σp(R) ⟕ S
//       （左连接保留全部左行；左表谓词在连接前后过滤等价。
//         注意：p 为 UNKNOWN 的左行在两种计划中都被筛掉，行为一致。）
//   P4  σp(R ⟖ S)，p 只引用右表列                   => R ⟖ σp(S)
//
// 明确不做的非法下推（反例见 counterexamples.ts）：
//   X1  把引用"补空侧"列的谓词推过外连接：
//       σp(R ⟕ S) 且 p 引用 S 的列 —— 推下去会把"未匹配补 NULL"的行提前筛掉，
//       而外连接语义要求这些行保留（补 NULL 后由上层谓词判定，UNKNOWN 才丢弃）。
//   X2  把谓词推过全外连接的任一侧。
//   X3  把引用双侧列的谓词拆分下推（只能留在连接之上）。
import type { Expr } from './expr.js';
import { and, conjuncts, referencedCols } from './expr.js';
import type { Plan } from './plan.js';
import { andOf } from './plan.js';

function subsetOf(a: Set<string>, b: string[]): boolean {
  for (const x of a) if (!b.includes(x)) return false;
  return true;
}

function outputColsOf(plan: Plan): string[] {
  switch (plan.kind) {
    case 'scan':
      return plan.table.schemaCols ??
        (plan.table.rows.length > 0 ? Object.keys(plan.table.rows[0]) : []);
    case 'filter':
      return outputColsOf(plan.input);
    case 'project':
      return plan.cols;
    case 'join':
      return [...outputColsOf(plan.left), ...outputColsOf(plan.right)];
  }
}

// 自底向上递归下推；返回优化后的计划。
export function pushDown(plan: Plan): Plan {
  switch (plan.kind) {
    case 'scan':
      return plan;
    case 'project':
      return { ...plan, input: pushDown(plan.input) };
    case 'join':
      return { ...plan, left: pushDown(plan.left), right: pushDown(plan.right) };
    case 'filter': {
      const input = pushDown(plan.input);
      return pushFilterThrough(plan.pred, input);
    }
  }
}

// 尝试把谓词 pred 推到 input 之下；推不下去就留在原地。
function pushFilterThrough(pred: Expr, input: Plan): Plan {
  if (input.kind === 'filter') {
    // P1：合并相邻筛选后继续。
    return pushFilterThrough(and(pred, input.pred), input.input);
  }

  if (input.kind === 'join') {
    const leftCols = outputColsOf(input.left);
    const rightCols = outputColsOf(input.right);
    const leftOnly: Expr[] = [];
    const rightOnly: Expr[] = [];
    const stay: Expr[] = [];

    for (const c of conjuncts(pred)) {
      const refs = referencedCols(c);
      if (subsetOf(refs, leftCols) && !refsIntersects(refs, rightCols)) leftOnly.push(c);
      else if (subsetOf(refs, rightCols) && !refsIntersects(refs, leftCols)) rightOnly.push(c);
      else stay.push(c);
    }

    let newLeft = input.left;
    let newRight = input.right;
    // P2/P3/P4：按连接类型决定哪些侧允许下推。
    const canPushLeft = input.joinType === 'inner' || input.joinType === 'left';
    const canPushRight = input.joinType === 'inner' || input.joinType === 'right';

    const pushedLeft = canPushLeft ? leftOnly : [];
    const pushedRight = canPushRight ? rightOnly : [];
    const keptLeft = canPushLeft ? [] : leftOnly;
    const keptRight = canPushRight ? [] : rightOnly;

    if (pushedLeft.length > 0) {
      newLeft = pushFilterThrough(andOf(pushedLeft), pushDown(input.left));
    }
    if (pushedRight.length > 0) {
      newRight = pushFilterThrough(andOf(pushedRight), pushDown(input.right));
    }

    const newJoin: Plan = { ...input, left: newLeft, right: newRight };
    const remaining = [...stay, ...keptLeft, ...keptRight];
    if (remaining.length === 0) return newJoin;
    return { kind: 'filter', pred: andOf(remaining), input: newJoin };
  }

  return { kind: 'filter', pred, input };
}

function refsIntersects(refs: Set<string>, cols: string[]): boolean {
  for (const r of refs) if (cols.includes(r)) return true;
  return false;
}
