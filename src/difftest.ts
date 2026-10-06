// 差分测试：同一随机程序分别在原解释器与“优化后解释器”上运行，
// 比较最终结果与完整错误轨迹（语句 id 序列 + 错误种类 + 出错节点 id）。

import { Program } from './ir';
import { run, Outcome, TraceEntry } from './interp';
import { optimize, countNodes } from './opt';
import { genProgram } from './randgen';

export function traceEqual(a: TraceEntry[], b: TraceEntry[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (x.type !== y.type) return false;
    if (x.type === 'stmt' && y.type === 'stmt') {
      if (x.id !== y.id) return false;
    } else if (x.type === 'error' && y.type === 'error') {
      if (x.id !== y.id || x.kind !== y.kind) return false;
    }
  }
  return true;
}

export function outcomeEqual(a: Outcome, b: Outcome): boolean {
  if (a.type !== b.type) return false;
  if (a.type === 'ok' && b.type === 'ok') {
    return a.value === b.value && traceEqual(a.trace, b.trace);
  }
  if (a.type === 'err' && b.type === 'err') {
    return a.kind === b.kind && a.at === b.at && traceEqual(a.trace, b.trace);
  }
  return false;
}

export interface Mismatch {
  seed: number;
  before: Outcome;
  after: Outcome;
  prog: Program;
  inputs: Record<string, number>;
}

export interface Stats {
  total: number;
  ok: number;
  overflow: number;
  divzero: number;
  nodesBefore: number;
  nodesAfter: number;
}

export function runFuzz(
  count: number,
  seedStart: number,
  onMismatch: (m: Mismatch) => void,
): Stats {
  const stats: Stats = {
    total: 0, ok: 0, overflow: 0, divzero: 0, nodesBefore: 0, nodesAfter: 0,
  };
  for (let i = 0; i < count; i++) {
    const seed = seedStart + i;
    const { prog, inputs } = genProgram(seed);
    const optProg = optimize(prog);
    const before = run(prog, inputs);
    const after = run(optProg, inputs);
    stats.total++;
    stats.nodesBefore += countNodes(prog);
    stats.nodesAfter += countNodes(optProg);
    if (before.type === 'ok') stats.ok++;
    else if (before.kind === 'overflow') stats.overflow++;
    else if (before.kind === 'divzero') stats.divzero++;
    if (!outcomeEqual(before, after)) {
      onMismatch({ seed, before, after, prog, inputs });
    }
  }
  return stats;
}
