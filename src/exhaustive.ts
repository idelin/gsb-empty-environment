/**
 * Exhaustive small-tree checker.
 *
 * For every ordered pair of generated trees it verifies:
 *  - diffTrees is deterministic (two runs produce identical ops),
 *  - the patch applied by the independent applier yields exactly the target,
 *  - the input trees are not mutated,
 *  - the result is flagged optimal when no budget is given,
 *  - tiny budgets still yield complete, applicable patches (optimal may be false).
 *
 * Deterministic tie-breaking makes the choice among equal-cost edit scripts
 * canonical; the golden tests below pin that choice down.
 */
import { diffTrees, TreeDiffError } from './diff';
import { applyPatch, PatchApplyError } from './apply';
import { PatchOp, TreeNode, TreeValue } from './types';

let failures = 0;
let checked = 0;

function fail(msg: string): void {
  failures++;
  console.error('FAIL: ' + msg);
}

function assert(cond: boolean, msg: string): void {
  if (!cond) fail(msg);
}

// ---------- canonical serialization (fixed key order, undefined-safe) ----------
function canonical(n: TreeNode): string {
  const v = n.value === undefined ? 'u' : 'v:' + JSON.stringify(n.value);
  const i = n.id === undefined ? 'u' : 'i:' + n.id;
  return '(' + n.type + '|' + v + '|' + i + '|' + n.children.map(canonical).join(',') + ')';
}

// ---------- shape generation ----------
interface Shape {
  kids: Shape[];
}

function genShapes(maxNodes: number): Shape[][] {
  const bySize: Shape[][] = [[]];
  const forests = (total: number): Shape[][] => {
    if (total === 0) return [[]];
    const out: Shape[][] = [];
    for (let k = 1; k <= total; k++) {
      for (const t of bySize[k]) {
        for (const rest of forests(total - k)) {
          out.push([t].concat(rest));
        }
      }
    }
    return out;
  };
  for (let n = 1; n <= maxNodes; n++) {
    bySize[n] = forests(n - 1).map((f) => ({ kids: f }));
  }
  return bySize;
}

function countNodes(s: Shape): number {
  let n = 1;
  for (const k of s.kids) n += countNodes(k);
  return n;
}

function decorate(
  shapes: Shape[],
  types: string[],
  values: Array<TreeValue | undefined>,
  useIds: boolean
): TreeNode[] {
  const out: TreeNode[] = [];
  for (const shape of shapes) {
    const n = countNodes(shape);
    const idChoices = useIds ? 2 : 1;
    const perNode = types.length * values.length * idChoices;
    const total = Math.pow(perNode, n);
    for (let code = 0; code < total; code++) {
      const digits: number[] = [];
      let rem = code;
      for (let i = 0; i < n; i++) {
        digits.push(rem % perNode);
        rem = Math.floor(rem / perNode);
      }
      let idx = 0;
      let pre = 0;
      const build = (s: Shape): TreeNode => {
        const d = digits[idx++];
        const preIndex = pre++;
        const t = types[Math.floor(d / (values.length * idChoices)) % types.length];
        const v = values[Math.floor(d / idChoices) % values.length];
        const hasId = useIds && d % 2 === 1;
        const node: TreeNode = { type: t, children: s.kids.map(build) };
        if (v !== undefined) node.value = v;
        if (hasId) node.id = 'n' + preIndex;
        return node;
      };
      out.push(build(shape));
    }
  }
  return out;
}

// ---------- pair checking ----------
const BUDGETS = [0, 1, 2, 3, 5, 10];

function cloneT(n: TreeNode): TreeNode {
  const out: TreeNode = { type: n.type, children: n.children.map(cloneT) };
  if (n.value !== undefined) out.value = n.value;
  if (n.id !== undefined) out.id = n.id;
  return out;
}

function checkPair(a: TreeNode, b: TreeNode): void {
  const snapA = canonical(a);
  const snapB = canonical(b);
  const r1 = diffTrees(a, b);
  const r2 = diffTrees(a, b);
  const r3 = diffTrees(cloneT(a), cloneT(b));
  if (JSON.stringify(r1.ops) !== JSON.stringify(r2.ops)) {
    fail('non-deterministic ops for ' + snapA + ' -> ' + snapB);
    return;
  }
  if (JSON.stringify(r3.ops) !== JSON.stringify(r1.ops)) {
    fail('ops depend on object identity for ' + snapA + ' -> ' + snapB);
    return;
  }
  if (r1.optimal !== true) {
    fail('optimal flag false without budget for ' + snapA + ' -> ' + snapB);
  }
  let out: TreeNode;
  try {
    out = applyPatch(a, r1.ops);
  } catch (e) {
    fail('applier rejected patch for ' + snapA + ' -> ' + snapB + ': ' + (e as Error).message);
    return;
  }
  if (canonical(out) !== snapB) {
    fail('patch mismatch for ' + snapA + ' -> ' + snapB + ' (got ' + canonical(out) + ')');
  }
  if (canonical(a) !== snapA || canonical(b) !== snapB) {
    fail('diffTrees mutated its inputs');
  }
  for (const budget of BUDGETS) {
    const rb = diffTrees(a, b, { budget });
    if (typeof rb.optimal !== 'boolean') {
      fail('optimal flag not boolean at budget ' + budget);
    }
    let outb: TreeNode;
    try {
      outb = applyPatch(a, rb.ops);
    } catch (e) {
      fail('applier rejected budget-' + budget + ' patch for ' + snapA + ' -> ' + snapB);
      continue;
    }
    if (canonical(outb) !== snapB) {
      fail('budget-' + budget + ' patch mismatch for ' + snapA + ' -> ' + snapB);
    }
  }
  checked++;
}

function checkAllPairs(trees: TreeNode[], label: string): void {
  for (const a of trees) {
    for (const b of trees) {
      checkPair(a, b);
    }
  }
  console.log(label + ': ' + trees.length + ' trees, ' + trees.length * trees.length + ' pairs checked');
}

// ---------- golden / unit tests ----------
function node(type: string, children: TreeNode[], value?: TreeValue, id?: string): TreeNode {
  const n: TreeNode = { type, children };
  if (value !== undefined) n.value = value;
  if (id !== undefined) n.id = id;
  return n;
}

function expectOps(label: string, a: TreeNode, b: TreeNode, expected: PatchOp[]): void {
  const r = diffTrees(a, b);
  const got = JSON.stringify(r.ops);
  const want = JSON.stringify(expected);
  if (got !== want) {
    fail(label + ': expected ops ' + want + ' but got ' + got);
  } else {
    checked++;
  }
}

function expectThrow(label: string, fn: () => void, errClass: Function): void {
  try {
    fn();
    fail(label + ': expected ' + errClass.name + ' but nothing was thrown');
  } catch (e) {
    if (e instanceof (errClass as never)) checked++;
    else fail(label + ': wrong error type: ' + e);
  }
}

function goldenTests(): void {
  // canonical tie-break: two equal-cost alignments, the diagonal-from-the-end wins
  expectOps(
    'tie-break keeps trailing alignment',
    node('R', [node('A', []), node('A', [])]),
    node('R', [node('A', [])]),
    [{ op: 'delete', path: [0] }]
  );
  // id match across parents produces exactly one move
  expectOps(
    'id move across parents',
    node('R', [node('P', [node('K', [], undefined, 'k')]), node('Q', [])]),
    node('R', [node('P', []), node('Q', [node('K', [], undefined, 'k')])]),
    [{ op: 'move', from: [0, 0], toParentPath: [1], toIndex: 0 }]
  );
  // sibling reorder via ids
  expectOps(
    'id reorder among siblings',
    node('R', [node('A', [], undefined, 'x'), node('B', [], undefined, 'y')]),
    node('R', [node('B', [], undefined, 'y'), node('A', [], undefined, 'x')]),
    [{ op: 'move', from: [1], toParentPath: [], toIndex: 0 }]
  );
  // update of value
  expectOps(
    'scalar update',
    node('R', [node('A', [], 1)]),
    node('R', [node('A', [], 2)]),
    [{ op: 'update', path: [0], type: 'A', value: 2 }]
  );
  // inverted id nesting is handled by ordered moves (inner node moves out first)
  expectOps(
    'inverted id nesting uses safe move order',
    node('R', [node('P', [node('K', [], undefined, 'k')], undefined, 'p')]),
    node('R', [node('K', [node('P', [], undefined, 'p')], undefined, 'k')]),
    [
      { op: 'move', from: [0, 0], toParentPath: [], toIndex: 0 },
      { op: 'move', from: [1], toParentPath: [0], toIndex: 0 },
    ]
  );
  // id match whose ancestors are unmatched falls back to delete+insert
  {
    const a = node('R', [node('X', [node('K', [], undefined, 'k')])]);
    const b = node('R', [node('K', [], undefined, 'k')]);
    const r = diffTrees(a, b);
    const out = applyPatch(a, r.ops);
    if (canonical(out) !== canonical(b)) fail('closure case: patch does not reach target');
    for (const op of r.ops) {
      if (op.op === 'move') fail('closure case: a move op was emitted');
    }
    checked++;
  }
  // budget interruption: complete patch, optimal = false
  {
    const a = node('R', [node('A', [node('B', []), node('C', [])]), node('D', [])]);
    const b = node('R', [node('A', [node('C', []), node('E', [])]), node('F', [])]);
    const r = diffTrees(a, b, { budget: 1 });
    if (r.optimal !== false) fail('budget case: optimal should be false');
    const out = applyPatch(a, r.ops);
    if (canonical(out) !== canonical(b)) fail('budget case: patch does not reach target');
    checked++;
  }
  // applier rejects invalid patches
  const base = node('R', [node('A', [node('B', [])])]);
  expectThrow('applier rejects cyclic move', () => {
    applyPatch(base, [{ op: 'move', from: [0], toParentPath: [0, 0], toIndex: 0 }]);
  }, PatchApplyError);
  expectThrow('applier rejects bad path', () => {
    applyPatch(base, [{ op: 'delete', path: [5] }]);
  }, PatchApplyError);
  expectThrow('applier rejects root delete', () => {
    applyPatch(base, [{ op: 'delete', path: [] }]);
  }, PatchApplyError);
  expectThrow('applier rejects duplicate ids in result', () => {
    applyPatch(node('R', [node('A', [], undefined, 'x')]), [
      { op: 'insert', parentPath: [], index: 1, node: node('B', [], undefined, 'x') },
    ]);
  }, PatchApplyError);
  expectThrow('diff rejects duplicate ids in input', () => {
    diffTrees(node('R', [node('A', [], undefined, 'x'), node('B', [], undefined, 'x')]), node('R', []));
  }, TreeDiffError);
}

// ---------- seeded random larger trees (move-heavy) ----------
function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomTree(rnd: () => number, maxDepth: number, idPool: number): TreeNode {
  const mk = (depth: number): TreeNode => {
    const n: TreeNode = { type: 'T' + Math.floor(rnd() * 3), children: [] };
    if (rnd() < 0.5) n.value = Math.floor(rnd() * 3);
    if (rnd() < 0.4) n.id = 'id' + Math.floor(rnd() * idPool);
    if (depth < maxDepth) {
      const k = Math.floor(rnd() * 4);
      for (let i = 0; i < k; i++) n.children.push(mk(depth + 1));
    }
    return n;
  };
  return mk(0);
}

function dedupeIds(n: TreeNode): void {
  const seen = new Set<string>();
  const walk = (x: TreeNode): void => {
    if (x.id !== undefined) {
      if (seen.has(x.id)) delete x.id;
      else seen.add(x.id);
    }
    for (const c of x.children) walk(c);
  };
  walk(n);
}

function randomSweep(pairs: number): void {
  const rnd = mulberry32(1234567);
  for (let i = 0; i < pairs; i++) {
    const a = randomTree(rnd, 4, 12);
    const b = randomTree(rnd, 4, 12);
    dedupeIds(a);
    dedupeIds(b);
    checkPair(a, b);
  }
  console.log('random sweep: ' + pairs + ' pairs checked');
}

// ---------- main ----------
function main(): void {
  goldenTests();

  const shapes = genShapes(4);
  const small = shapes[1].concat(shapes[2], shapes[3]);
  const big = shapes[4];

  // sweep 1: types + values, no ids, all pairs up to 3 nodes
  const sweep1 = decorate(small, ['A', 'B'], [undefined, 0], false);
  checkAllPairs(sweep1, 'sweep 1 (types+values, n<=3)');

  // sweep 2: types + stable ids, all pairs up to 3 nodes
  const sweep2 = decorate(small, ['A', 'B'], [undefined], true);
  checkAllPairs(sweep2, 'sweep 2 (types+ids, n<=3)');

  // sweep 3: types + values + ids, all pairs up to 2 nodes
  const sweep3 = decorate(shapes[1].concat(shapes[2]), ['A', 'B'], [undefined, 0], true);
  checkAllPairs(sweep3, 'sweep 3 (types+values+ids, n<=2)');

  // sweep 4: 4-node trees, deterministic sampled pairs
  const sweep4 = decorate(big, ['A', 'B'], [undefined, 0], false);
  const stride = 37;
  for (let i = 0; i < sweep4.length; i++) {
    checkPair(sweep4[i], sweep4[(i * stride + 11) % sweep4.length]);
    checkPair(sweep4[i], sweep4[i]);
  }
  console.log('sweep 4 (n=4 sampled): ' + sweep4.length + ' trees');

  randomSweep(300);

  console.log('----');
  console.log('pairs checked: ' + checked + ', failures: ' + failures);
  if (failures > 0) {
    process.exit(1);
  }
  console.log('ALL OK');
}

main();
