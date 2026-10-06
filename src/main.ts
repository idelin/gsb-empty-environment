// 入口：定向测试 + 随机差分模糊测试。

import { Expr, Stmt, Program, BinOp, I32_MIN, I32_MAX } from './ir';
import { run, Outcome } from './interp';
import { optimize, countNodes } from './opt';
import { runFuzz, outcomeEqual, Mismatch } from './difftest';

let nextId = 1;
const id = () => nextId++;
const c = (value: number): Expr => ({ kind: 'const', id: id(), value });
const v = (name: string): Expr => ({ kind: 'var', id: id(), name });
const bin = (op: BinOp, l: Expr, r: Expr): Expr => ({ kind: 'bin', id: id(), op, l, r });
const neg = (e: Expr): Expr => ({ kind: 'neg', id: id(), e });
const assign = (name: string, e: Expr): Stmt => ({ kind: 'assign', id: id(), name, e });
const iff = (cond: Expr, then: Stmt[], els: Stmt[]): Stmt => ({ kind: 'if', id: id(), cond, then, els });
const ret = (e: Expr): Stmt => ({ kind: 'return', id: id(), e });

function containsErrorNode(prog: Program, nodeId: number): boolean {
  const walkE = (e: Expr): boolean => {
    if (e.id === nodeId) return true;
    if (e.kind === 'neg') return walkE(e.e);
    if (e.kind === 'bin') return walkE(e.l) || walkE(e.r);
    return false;
  };
  const walkS = (ss: Stmt[]): boolean =>
    ss.some((s) => {
      if (s.kind === 'assign' || s.kind === 'return') return walkE(s.e);
      return walkE(s.cond) || walkS(s.then) || walkS(s.els);
    });
  return walkS(prog.body);
}

interface Case {
  name: string;
  prog: Program;
  inputs: Record<string, number>;
  expect: (o: Outcome) => boolean;
  extraCheck?: (opt: Program) => boolean;
}

function buildCases(): Case[] {
  const cases: Case[] = [];

  // 1. x*0 不得消除 x 的求值异常（除零）
  {
    const divExpr = bin('div', c(1), v('z'));
    const mulZero = bin('mul', divExpr, c(0));
    const divId = divExpr.id;
    cases.push({
      name: 'keep-divzero-under-mul-zero',
      prog: { inputs: ['z'], body: [assign('t', mulZero), ret(v('t'))] },
      inputs: { z: 0 },
      expect: (o) => o.type === 'err' && o.kind === 'divzero' && o.at === divId,
      extraCheck: (opt) => containsErrorNode(opt, divId),
    });
  }

  // 2. 0*x 同理（溢出）
  {
    const addExpr = bin('add', c(I32_MAX), c(1));
    const mulZero = bin('mul', c(0), addExpr);
    const addId = addExpr.id;
    cases.push({
      name: 'keep-overflow-under-zero-mul',
      prog: { inputs: [], body: [assign('t', mulZero), ret(v('t'))] },
      inputs: {},
      expect: (o) => o.type === 'err' && o.kind === 'overflow' && o.at === addId,
      extraCheck: (opt) => containsErrorNode(opt, addId),
    });
  }

  // 3. 0/e 不得折叠（e 可能为 0 -> 除零）
  {
    const divExpr = bin('div', c(0), v('z'));
    const divId = divExpr.id;
    cases.push({
      name: 'keep-zero-div',
      prog: { inputs: ['z'], body: [assign('t', divExpr), ret(v('t'))] },
      inputs: { z: 0 },
      expect: (o) => o.type === 'err' && o.kind === 'divzero' && o.at === divId,
      extraCheck: (opt) => containsErrorNode(opt, divId),
    });
  }

  // 4. 会出错的常量表达式不折叠：I32_MIN / -1 运行时溢出，位置不变
  {
    const divExpr = bin('div', c(I32_MIN), c(-1));
    const divId = divExpr.id;
    cases.push({
      name: 'keep-const-div-overflow',
      prog: { inputs: [], body: [assign('t', divExpr), ret(v('t'))] },
      inputs: {},
      expect: (o) => o.type === 'err' && o.kind === 'overflow' && o.at === divId,
      extraCheck: (opt) => containsErrorNode(opt, divId),
    });
  }

  // 5. -I32_MIN 取负溢出
  {
    const negExpr = neg(c(I32_MIN));
    const negId = negExpr.id;
    cases.push({
      name: 'keep-const-neg-overflow',
      prog: { inputs: [], body: [assign('t', negExpr), ret(v('t'))] },
      inputs: {},
      expect: (o) => o.type === 'err' && o.kind === 'overflow' && o.at === negId,
      extraCheck: (opt) => containsErrorNode(opt, negId),
    });
  }

  // 6. 常量分支：删除确定不可达分支，结果与轨迹一致
  {
    const deadAssign = assign('d', bin('div', c(1), c(0))); // 死分支里即使有错也不执行
    const prog: Program = {
      inputs: ['z'],
      body: [
        assign('k', c(5)),
        iff(bin('lt', v('k'), c(10)), [assign('t', bin('add', v('z'), c(1)))], [deadAssign]),
        ret(v('t')),
      ],
    };
    cases.push({
      name: 'prune-const-branch',
      prog,
      inputs: { z: 41 },
      expect: (o) => o.type === 'ok' && o.value === 42,
      extraCheck: (opt) => countNodes(opt) < countNodes(prog),
    });
  }

  // 7. return 之后的死代码被删除
  {
    const prog: Program = {
      inputs: [],
      body: [
        ret(c(7)),
        assign('dead', bin('div', c(1), c(0))),
      ],
    };
    cases.push({
      name: 'prune-dead-after-return',
      prog,
      inputs: {},
      expect: (o) => o.type === 'ok' && o.value === 7,
      extraCheck: (opt) => countNodes(opt) < countNodes(prog),
    });
  }

  // 8. 恒等元化简保留求值： (1/z) + 0 仍除零
  {
    const divExpr = bin('div', c(1), v('z'));
    const addZero = bin('add', divExpr, c(0));
    const divId = divExpr.id;
    cases.push({
      name: 'identity-keeps-eval',
      prog: { inputs: ['z'], body: [assign('t', addZero), ret(v('t'))] },
      inputs: { z: 0 },
      expect: (o) => o.type === 'err' && o.kind === 'divzero' && o.at === divId,
      extraCheck: (opt) => containsErrorNode(opt, divId),
    });
  }

  // 9. 常量传播跨分支汇合：两个分支赋不同值 -> 汇合后不是常量
  {
    const prog: Program = {
      inputs: ['z'],
      body: [
        iff(bin('gt', v('z'), c(0)), [assign('t', c(1))], [assign('t', c(2))]),
        assign('u', bin('mul', v('t'), c(0))), // t 非常量，但 u = t*0 仍不得折叠掉 t
        ret(v('u')),
      ],
    };
    cases.push({
      name: 'merge-not-const',
      prog,
      inputs: { z: 5 },
      expect: (o) => o.type === 'ok' && o.value === 0,
    });
  }

  return cases;
}

function fmtOutcome(o: Outcome): string {
  if (o.type === 'ok') return `ok(${o.value})`;
  return `err(${o.kind}@#${o.at})`;
}

function main(): void {
  const args = process.argv.slice(2);
  const fuzzCount = args.length > 0 ? parseInt(args[0], 10) : 5000;
  const seedStart = args.length > 1 ? parseInt(args[1], 10) : 1;

  // ---- 定向测试 ----
  let failed = 0;
  for (const tc of buildCases()) {
    const opt = optimize(tc.prog);
    const before = run(tc.prog, tc.inputs);
    const after = run(opt, tc.inputs);
    const same = outcomeEqual(before, after);
    const expectOk = tc.expect(before);
    const extraOk = tc.extraCheck ? tc.extraCheck(opt) : true;
    if (same && expectOk && extraOk) {
      console.log(`PASS ${tc.name}  (${fmtOutcome(before)})`);
    } else {
      failed++;
      console.log(`FAIL ${tc.name}`);
      console.log(`  before=${fmtOutcome(before)} after=${fmtOutcome(after)} same=${same} expect=${expectOk} extra=${extraOk}`);
    }
  }

  // ---- 随机差分模糊测试 ----
  const mismatches: Mismatch[] = [];
  const stats = runFuzz(fuzzCount, seedStart, (m) => {
    mismatches.push(m);
    if (mismatches.length <= 5) {
      console.log(`MISMATCH seed=${m.seed}`);
      console.log(`  before=${fmtOutcome(m.before)} trace=${JSON.stringify(m.before.trace)}`);
      console.log(`  after =${fmtOutcome(m.after)} trace=${JSON.stringify(m.after.trace)}`);
    }
  });

  console.log('---');
  console.log(`fuzz: ${stats.total} programs (seeds ${seedStart}..${seedStart + stats.total - 1})`);
  console.log(`  outcomes: ok=${stats.ok} overflow=${stats.overflow} divzero=${stats.divzero}`);
  console.log(`  nodes: before=${stats.nodesBefore} after=${stats.nodesAfter} (-${(100 * (1 - stats.nodesAfter / stats.nodesBefore)).toFixed(1)}%)`);
  console.log(`  mismatches: ${mismatches.length}`);
  console.log(`directed failures: ${failed}`);

  if (failed > 0 || mismatches.length > 0) process.exit(1);
  console.log('ALL GOOD');
}

main();
