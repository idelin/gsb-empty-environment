// Cross-validation: the symbolic SAT engine and the independent exhaustive
// interpreter must agree on a systematically enumerated set of expressions.
// The exhaustive engine enumerates the entire input space, so its verdict is
// a proof at these widths -- exhaustive verification, not random sampling.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { kBin, kConst, kIte, kNeg, kNot, kVar } from '../src/ast.ts';
import type { BinOp, Expr } from '../src/ast.ts';
import { symbolicCheck } from '../src/checker.ts';
import { exhaustiveCheck } from '../src/exhaustive.ts';
import { evaluate } from '../src/interp.ts';

function buildCorpus(): Expr[] {
  const atoms: Expr[] = [kVar('x'), kVar('y'), kConst(0n), kConst(1n), kConst(3n)];
  const ops: BinOp[] = ['and', 'or', 'xor', 'add', 'sub', 'shl', 'shr'];
  const out: Expr[] = [...atoms];
  for (const a of atoms) {
    out.push(kNot(a), kNeg(a));
  }
  for (const op of ops) {
    for (const a of atoms) {
      for (const b of atoms) {
        out.push(kBin(op, a, b));
      }
    }
  }
  const depth1 = out.slice(atoms.length);
  for (let i = 0; i < depth1.length; i += 3) {
    out.push(kBin('add', depth1[i], kVar('x')));
    out.push(kBin('xor', depth1[i], kVar('y')));
    out.push(kIte(depth1[i], kVar('x'), kVar('y')));
  }
  return out;
}

const corpus = buildCorpus();

test('symbolic engine agrees with exhaustive proof on the whole corpus', () => {
  const widths = [1, 2, 3, 4, 5];
  const pairs: Array<[number, number]> = [];
  for (let i = 0; i < corpus.length; i++) {
    pairs.push([i, i]);
    pairs.push([i, (i + 1) % corpus.length]);
    pairs.push([i, (i + 7) % corpus.length]);
  }
  let checked = 0;
  for (const width of widths) {
    for (const [i, j] of pairs) {
      const a = corpus[i];
      const b = corpus[j];
      const sym = symbolicCheck(a, b, width);
      const exh = exhaustiveCheck(a, b, width);
      assert.notEqual(sym.status, 'unknown', `symbolic unknown @${width} #${i}#${j}`);
      assert.notEqual(exh.status, 'unknown', `exhaustive unknown @${width} #${i}#${j}`);
      assert.equal(sym.status, exh.status, `status mismatch @${width} pair (${i}, ${j})`);
      if (sym.status === 'not-equal' && exh.status === 'not-equal') {
        assert.deepEqual(
          sym.counterexample,
          exh.counterexample,
          `counterexample mismatch @${width} pair (${i}, ${j})`,
        );
        assert.notEqual(
          evaluate(a, sym.counterexample, width),
          evaluate(b, sym.counterexample, width),
        );
      }
      checked++;
    }
  }
  assert.ok(checked > 0);
});

test('exhaustive engine reports unknown instead of guessing on huge spaces', () => {
  const r = exhaustiveCheck(kVar('x'), kVar('y'), 32, 20);
  assert.equal(r.status, 'unknown');
});
