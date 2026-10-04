// 非法谓词下推的反例。
//
// 核心原因：外连接会为未匹配的行补 NULL，而这些"补出来的 NULL"与真实 NULL
// 遵循同一套三值规则——比较得 UNKNOWN，筛选只保留 TRUE。
// 若把引用补空侧列的谓词推到连接之下，NULL 补全发生在筛选之后，
// 本应保留的"未匹配左行"就永远不会出现（或反之），结果集不同。
import type { Row } from './expr.js';
import { col, cmp, lit } from './expr.js';
import type { Plan } from './plan.js';
import { execute, filterNode, joinNode, scan } from './plan.js';
import { sameMultiset } from './equiv.js';

export interface Counterexample {
  name: string;
  description: string;
  left: Row[];
  right: Row[];
  before: Row[]; // 正确计划（谓词在连接之上）的结果
  after: Row[];  // 非法下推后的结果
}

// X1：σ_{R.y > 1}(L ⟕ R)  ≠  L ⟕ σ_{R.y > 1}(R)
// L 中 k=2 的行在 R 中无匹配，应补 NULL 保留（R.y>1 为 UNKNOWN 被上层筛掉，
// 但推下去之后该行对应的 R 侧整行消失，左行反而被保留下来——结果相反）。
export function cxPushPredicateToNullSupplyingSide(): Counterexample {
  const left: Row[] = [{ k: 1, a: 10 }, { k: 2, a: 20 }];
  const right: Row[] = [{ k: 1, y: 0 }, { k: 3, y: 5 }];

  const pred = cmp('>', col('y'), lit(1));
  const correct: Plan = filterNode(pred, joinNode('left', ['k'], ['k'], scan({ name: 'L', rows: left }), scan({ name: 'R', rows: right })));
  const illegalPushed: Plan = joinNode(
    'left', ['k'], ['k'],
    scan({ name: 'L', rows: left }),
    { kind: 'filter', pred, input: scan({ name: 'R', rows: right }) },
  );

  return {
    name: 'X1 谓词推入左连接的补空侧（右侧）',
    description:
      'σ_{y>1}(L ⟕ R) 推到右侧变成 L ⟕ σ_{y>1}(R)：' +
      'k=2 的左行本应补 NULL 后被上层 UNKNOWN 筛掉；' +
      '下推后 R 侧 k=1 的行被提前过滤，k=1 的左行反而补 NULL 存活路径不同，结果集改变。',
    left,
    right,
    before: execute(correct),
    after: execute(illegalPushed),
  };
}

// X2：σ_{L.a > 0}(L ⟖ R) ≠ σ_{L.a > 0}(L) ⟖ R（对称地，推入保留侧的对面也错）
// 这里展示：右连接中把左表谓词推下去，未匹配右行补的左 NULL 行会丢失/多出。
export function cxPushAcrossPreservedSideOfRightJoin(): Counterexample {
  const left: Row[] = [{ k: 1, a: 10 }, { k: 2, a: -1 }];
  const right: Row[] = [{ k: 1, y: 7 }, { k: 9, y: 8 }];

  const pred = cmp('>', col('a'), lit(0));
  const correct: Plan = filterNode(pred, joinNode('right', ['k'], ['k'], scan({ name: 'L', rows: left }), scan({ name: 'R', rows: right })));
  const illegalPushed: Plan = joinNode(
    'right', ['k'], ['k'],
    { kind: 'filter', pred, input: scan({ name: 'L', rows: left }) },
    scan({ name: 'R', rows: right }),
  );

  return {
    name: 'X2 谓词推过右连接的左侧（非保留侧）',
    description:
      'σ_{a>0}(L ⟖ R) 推到左侧：k=9 的右行未匹配，左列补 NULL，a>0 为 UNKNOWN 应被筛掉；' +
      '但 k=2 的左行 a=-1 被提前过滤后，k=2 若被其他右行匹配的行为也会改变（此处体现为行数/内容差异）。',
    left,
    right,
    before: execute(correct),
    after: execute(illegalPushed),
  };
}

// X3：σ_{L.a > 0}(L ⊻ R)（全外连接）任何一侧都不可推。
export function cxPushBelowFullOuterJoin(): Counterexample {
  const left: Row[] = [{ k: 1, a: 10 }, { k: 2, a: -1 }];
  const right: Row[] = [{ k: 2, y: 7 }, { k: 3, y: 8 }];

  const pred = cmp('>', col('a'), lit(0));
  const correct: Plan = filterNode(pred, joinNode('full', ['k'], ['k'], scan({ name: 'L', rows: left }), scan({ name: 'R', rows: right })));
  const illegalPushed: Plan = joinNode(
    'full', ['k'], ['k'],
    { kind: 'filter', pred, input: scan({ name: 'L', rows: left }) },
    scan({ name: 'R', rows: right }),
  );

  return {
    name: 'X3 谓词推过全外连接',
    description:
      'σ_{a>0}(L ⊻ R) 推到左侧：k=2 的左行 a=-1 本应在连接后与 R.k=2 匹配、再被上层筛掉；' +
      '提前过滤后 R.k=2 变成未匹配行，补左 NULL 后反而以 (NULL,NULL,2,7) 的形式出现在结果中。',
    left,
    right,
    before: execute(correct),
    after: execute(illegalPushed),
  };
}

export function allCounterexamples(): Counterexample[] {
  return [cxPushPredicateToNullSupplyingSide(), cxPushAcrossPreservedSideOfRightJoin(), cxPushBelowFullOuterJoin()];
}

export interface CxCheck {
  cx: Counterexample;
  differs: boolean;
}

export function verifyCounterexamples(): CxCheck[] {
  return allCounterexamples().map((cx) => ({
    cx,
    differs: !sameMultiset(cx.before, cx.after),
  }));
}
