import test from 'node:test';
import assert from 'node:assert/strict';
import {
  analyzeEditDistance,
  editDistance,
  kthOptimalPath,
  DEFAULT_COSTS,
  DEFAULT_MOVE_ORDER,
} from '../src/index.ts';
import type { EditOperation, MoveKind, OperationCosts } from '../src/index.ts';

interface BruteForceResult {
  cost: number;
  paths: EditOperation[][];
}

function bruteForceOptimalPaths(
  source: string,
  target: string,
  costs: OperationCosts,
  moveOrder: readonly MoveKind[],
): BruteForceResult {
  const s = Array.from(source);
  const t = Array.from(target);
  const m = s.length;
  const n = t.length;
  const costMemo = new Map<string, number>();

  const cellCost = (i: number, j: number): number => {
    const key = `${i},${j}`;
    const hit = costMemo.get(key);
    if (hit !== undefined) return hit;
    let best = Infinity;
    if (i === 0 && j === 0) {
      best = 0;
    } else {
      if (i > 0 && j > 0) {
        best = Math.min(best, cellCost(i - 1, j - 1) + (s[i - 1] === t[j - 1] ? 0 : costs.substitute));
      }
      if (i > 0) best = Math.min(best, cellCost(i - 1, j) + costs.delete);
      if (j > 0) best = Math.min(best, cellCost(i, j - 1) + costs.insert);
    }
    costMemo.set(key, best);
    return best;
  };

  const pathsFrom = (i: number, j: number): EditOperation[][] => {
    if (i === 0 && j === 0) return [[]];
    const here = cellCost(i, j);
    const out: EditOperation[][] = [];
    for (const move of moveOrder) {
      if (move === 'diagonal') {
        if (i === 0 || j === 0) continue;
        const matched = s[i - 1] === t[j - 1];
        const opCost = matched ? 0 : costs.substitute;
        if (cellCost(i - 1, j - 1) + opCost !== here) continue;
        const op: EditOperation = {
          kind: matched ? 'match' : 'substitute',
          sourceIndex: i - 1,
          targetIndex: j - 1,
          sourceChar: s[i - 1],
          targetChar: t[j - 1],
          cost: opCost,
        };
        for (const prefix of pathsFrom(i - 1, j - 1)) out.push([...prefix, op]);
      } else if (move === 'delete') {
        if (i === 0) continue;
        if (cellCost(i - 1, j) + costs.delete !== here) continue;
        const op: EditOperation = {
          kind: 'delete',
          sourceIndex: i - 1,
          targetIndex: j,
          sourceChar: s[i - 1],
          targetChar: undefined,
          cost: costs.delete,
        };
        for (const prefix of pathsFrom(i - 1, j)) out.push([...prefix, op]);
      } else {
        if (j === 0) continue;
        if (cellCost(i, j - 1) + costs.insert !== here) continue;
        const op: EditOperation = {
          kind: 'insert',
          sourceIndex: i,
          targetIndex: j - 1,
          sourceChar: undefined,
          targetChar: t[j - 1],
          cost: costs.insert,
        };
        for (const prefix of pathsFrom(i, j - 1)) out.push([...prefix, op]);
      }
    }
    return out;
  };

  return { cost: cellCost(m, n), paths: pathsFrom(m, n) };
}

function replay(source: string, ops: EditOperation[]): string {
  const s = Array.from(source);
  const out: string[] = [];
  let cursor = 0;
  for (const op of ops) {
    if (op.kind === 'insert') {
      assert.equal(op.sourceIndex, cursor);
      assert.equal(op.sourceChar, undefined);
      out.push(op.targetChar as string);
    } else {
      assert.equal(op.sourceIndex, cursor);
      assert.equal(op.sourceChar, s[cursor]);
      cursor++;
      if (op.kind === 'match') {
        assert.equal(op.sourceChar, op.targetChar);
        out.push(op.sourceChar as string);
      } else if (op.kind === 'substitute') {
        out.push(op.targetChar as string);
      } else {
        assert.equal(op.targetChar, undefined);
      }
    }
  }
  assert.equal(cursor, s.length);
  return out.join('');
}

function pathCost(ops: EditOperation[]): number {
  return ops.reduce((sum, op) => sum + op.cost, 0);
}

function strings(alphabet: string[], maxLength: number): string[] {
  const out = [''];
  for (let length = 1; length <= maxLength; length++) {
    const prev = out.filter((w) => w.length === length - 1);
    for (const word of prev) {
      for (const ch of alphabet) out.push(word + ch);
    }
  }
  return out;
}

function binom(n: bigint, k: bigint): bigint {
  let result = 1n;
  for (let i = 0n; i < k; i++) {
    result = (result * (n - i)) / (i + 1n);
  }
  return result;
}

test('cost validation: zero-cost insert/delete forbidden, zero-cost substitute allowed', () => {
  assert.throws(() => editDistance('a', 'b', { insert: 0, delete: 1, substitute: 1 }), RangeError);
  assert.throws(() => editDistance('a', 'b', { insert: 1, delete: 0, substitute: 1 }), RangeError);
  assert.throws(() => editDistance('a', 'b', { insert: -1, delete: 1, substitute: 1 }), RangeError);
  assert.throws(() => editDistance('a', 'b', { insert: 1, delete: 1, substitute: -1 }), RangeError);
  assert.throws(() => editDistance('a', 'b', { insert: NaN, delete: 1, substitute: 1 }), RangeError);
  assert.throws(() => editDistance('a', 'b', { insert: 1, delete: Infinity, substitute: 1 }), RangeError);
  assert.throws(
    () => editDistance('a', 'b', { insert: '1' as unknown as number, delete: 1, substitute: 1 }),
    TypeError,
  );
  const ok = editDistance('a', 'b', { insert: 1, delete: 1, substitute: 0 });
  assert.equal(ok.cost, 0);
  assert.equal(ok.count, 1n);
});

test('moveOrder validation and public default order', () => {
  assert.deepEqual([...DEFAULT_MOVE_ORDER], ['diagonal', 'delete', 'insert']);
  assert.throws(
    () => analyzeEditDistance('a', 'b', DEFAULT_COSTS, { moveOrder: ['delete', 'delete', 'insert'] }),
    TypeError,
  );
  assert.throws(
    () => analyzeEditDistance('a', 'b', DEFAULT_COSTS, { moveOrder: ['diagonal'] }),
    TypeError,
  );
});

test('empty and trivial inputs', () => {
  const empty = analyzeEditDistance('', '');
  assert.equal(empty.cost, 0);
  assert.equal(empty.count, 1n);
  assert.deepEqual(empty.unrank(0), []);
  assert.equal(editDistance('', 'ab').cost, 2);
  assert.equal(editDistance('', 'ab').count, 1n);
  assert.equal(editDistance('ab', '').cost, 2);
  assert.equal(editDistance('ab', '').count, 1n);
  assert.equal(editDistance('same', 'same').cost, 0);
  assert.equal(editDistance('same', 'same').count, 1n);
});

test('repeated symbols: distinct paths, same final string', () => {
  const analysis = analyzeEditDistance('aa', 'a');
  assert.equal(analysis.cost, 1);
  assert.equal(analysis.count, 2n);
  const first = analysis.unrank(0);
  const second = analysis.unrank(1);
  assert.notDeepEqual(first, second);
  assert.equal(first.find((op) => op.kind === 'delete')?.sourceIndex, 0);
  assert.equal(second.find((op) => op.kind === 'delete')?.sourceIndex, 1);
  assert.equal(replay('aa', first), 'a');
  assert.equal(replay('aa', second), 'a');
});

test('zero-cost substitution creates multiple optimal paths', () => {
  const costs = { insert: 1, delete: 1, substitute: 0 };
  const analysis = analyzeEditDistance('ab', 'b', costs);
  assert.equal(analysis.cost, 1);
  assert.equal(analysis.count, 2n);
  const serialized = [analysis.unrank(0), analysis.unrank(1)]
    .map((ops) => ops.map((op) => `${op.kind}@${op.sourceIndex}`).join('|'))
    .sort();
  assert.deepEqual(serialized, ['delete@0|match@1', 'substitute@0|delete@1']);
  assert.equal(replay('ab', analysis.unrank(0)), 'b');
  assert.equal(replay('ab', analysis.unrank(1)), 'b');
});

test('unrank is deterministic and boundary order is public', () => {
  const costs = { insert: 1, delete: 1, substitute: 0 };
  const defaultAnalysis = analyzeEditDistance('ab', 'b', costs);
  assert.deepEqual(defaultAnalysis.unrank(0), defaultAnalysis.unrank(0));
  assert.equal(defaultAnalysis.unrank(0)[0].kind, 'delete');
  assert.equal(defaultAnalysis.unrank(1)[0].kind, 'substitute');

  const deleteFirst = analyzeEditDistance('ab', 'b', costs, {
    moveOrder: ['delete', 'insert', 'diagonal'],
  });
  assert.equal(deleteFirst.cost, 1);
  assert.equal(deleteFirst.count, 2n);
  assert.equal(deleteFirst.unrank(0)[0].kind, 'substitute');
  assert.equal(deleteFirst.unrank(1)[0].kind, 'delete');

  const defaultSet = new Set(
    [0, 1].map((k) => JSON.stringify(defaultAnalysis.unrank(k))),
  );
  const customSet = new Set(
    [0, 1].map((k) => JSON.stringify(deleteFirst.unrank(k))),
  );
  assert.deepEqual(customSet, defaultSet);
});

test('unrank index bounds', () => {
  const analysis = analyzeEditDistance('aa', 'a');
  assert.throws(() => analysis.unrank(2), RangeError);
  assert.throws(() => analysis.unrank(-1), RangeError);
  assert.throws(() => analysis.unrank(2n), RangeError);
  assert.throws(() => analysis.unrank(1.5), TypeError);
  assert.throws(() => analysis.unrank(2 ** 53), TypeError);
  assert.equal(analysis.unrank(1n).find((op) => op.kind === 'delete')?.sourceIndex, 1);
});

test('count uses bigint beyond 2^53-safe range', () => {
  const source = 'a'.repeat(60);
  const target = 'a'.repeat(30);
  const analysis = analyzeEditDistance(source, target);
  assert.equal(analysis.cost, 30);
  assert.equal(typeof analysis.count, 'bigint');
  assert.equal(analysis.count, binom(60n, 30n));
  assert.equal(analysis.count, 118264581564861424n);
  const last = analysis.unrank(analysis.count - 1n);
  assert.equal(replay(source, last), target);
  assert.equal(pathCost(last), 30);
  assert.throws(() => analysis.unrank(analysis.count), RangeError);
});

test('kthOptimalPath wrapper matches analysis.unrank', () => {
  const analysis = analyzeEditDistance('kitten', 'sitting');
  assert.equal(analysis.cost, 3);
  for (let k = 0n; k < analysis.count; k++) {
    assert.deepEqual(kthOptimalPath('kitten', 'sitting', k), analysis.unrank(k));
  }
});

test('exhaustive cross-check vs brute-force enumeration (alphabet {a,b}, length <= 4)', () => {
  const words = strings(['a', 'b'], 4);
  const costSets: OperationCosts[] = [
    { insert: 1, delete: 1, substitute: 1 },
    { insert: 1, delete: 1, substitute: 0 },
    { insert: 2, delete: 1, substitute: 1 },
    { insert: 1, delete: 3, substitute: 2 },
    { insert: 1, delete: 1, substitute: 5 },
  ];
  for (const costs of costSets) {
    for (const source of words) {
      for (const target of words) {
        const expected = bruteForceOptimalPaths(source, target, costs, DEFAULT_MOVE_ORDER);
        const analysis = analyzeEditDistance(source, target, costs);
        assert.equal(analysis.cost, expected.cost, `cost ${source}->${target} ${JSON.stringify(costs)}`);
        assert.equal(
          analysis.count,
          BigInt(expected.paths.length),
          `count ${source}->${target} ${JSON.stringify(costs)}`,
        );
        const seen = new Set<string>();
        for (let k = 0; k < expected.paths.length; k++) {
          const path = analysis.unrank(k);
          assert.deepEqual(
            path,
            expected.paths[k],
            `path #${k} ${source}->${target} ${JSON.stringify(costs)}`,
          );
          const key = JSON.stringify(path);
          assert.ok(!seen.has(key), `duplicate path #${k} ${source}->${target}`);
          seen.add(key);
          assert.equal(replay(source, path), target);
          assert.equal(pathCost(path), expected.cost);
        }
        assert.throws(() => analysis.unrank(expected.paths.length), RangeError);
      }
    }
  }
});

test('exhaustive cross-check vs brute-force enumeration (alphabet {a,b,c}, length <= 3)', () => {
  const words = strings(['a', 'b', 'c'], 3);
  const costSets: OperationCosts[] = [
    { insert: 1, delete: 1, substitute: 1 },
    { insert: 1, delete: 1, substitute: 0 },
    { insert: 3, delete: 2, substitute: 1 },
  ];
  for (const costs of costSets) {
    for (const source of words) {
      for (const target of words) {
        const expected = bruteForceOptimalPaths(source, target, costs, DEFAULT_MOVE_ORDER);
        const analysis = analyzeEditDistance(source, target, costs);
        assert.equal(analysis.cost, expected.cost);
        assert.equal(analysis.count, BigInt(expected.paths.length));
        for (let k = 0; k < expected.paths.length; k++) {
          assert.deepEqual(analysis.unrank(k), expected.paths[k]);
          assert.equal(replay(source, analysis.unrank(k)), target);
        }
      }
    }
  }
});

test('custom moveOrder matches brute-force enumeration in that order', () => {
  const words = strings(['a', 'b'], 3);
  const costs: OperationCosts = { insert: 1, delete: 1, substitute: 0 };
  const order: MoveKind[] = ['insert', 'delete', 'diagonal'];
  for (const source of words) {
    for (const target of words) {
      const expected = bruteForceOptimalPaths(source, target, costs, order);
      const analysis = analyzeEditDistance(source, target, costs, { moveOrder: order });
      assert.equal(analysis.count, BigInt(expected.paths.length));
      for (let k = 0; k < expected.paths.length; k++) {
        assert.deepEqual(analysis.unrank(k), expected.paths[k]);
      }
    }
  }
});
