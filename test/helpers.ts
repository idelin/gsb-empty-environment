import assert from 'node:assert/strict';
import { Program } from '../src/ast.ts';
import { AttrEvaluator } from '../src/evaluator.ts';
import { registerLanguage, snapshotIncremental } from '../src/lang.ts';
import type { Diagnostic } from '../src/lang.ts';
import { evaluateReference } from '../src/reference.ts';

export function setup(visibility: 'sequential' | 'block' = 'sequential') {
  const prog = new Program();
  const ev = new AttrEvaluator();
  registerLanguage(ev, prog, { visibility });
  return { prog, ev };
}

/** 增量求值器的全树快照必须与参考求值器（从头遍历）完全一致。 */
export function assertMatchesReference(prog: Program, ev: AttrEvaluator): void {
  const inc = snapshotIncremental(prog, ev);
  const ref = evaluateReference(prog);
  assert.deepStrictEqual(inc, ref);
}

export function rootErrors(prog: Program, ev: AttrEvaluator): Diagnostic[] {
  if (!prog.root) throw new Error('no root');
  return ev.get(prog.root.id, 'errors') as Diagnostic[];
}
