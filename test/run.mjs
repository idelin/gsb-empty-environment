import assert from 'node:assert/strict';
import { allocateResources } from '../build/allocator.mjs';

let passed = 0;
const failures = [];

function test(name, fn) {
  try {
    fn();
    passed++;
    console.log('ok   - ' + name);
  } catch (err) {
    failures.push([name, err]);
    console.log('FAIL - ' + name);
  }
}

function ov(a, b) {
  if (a.end <= a.start || b.end <= b.start) return false;
  return a.start < b.end && b.start < a.end;
}

function maxOverlapOf(items) {
  const ev = [];
  for (const it of items) {
    if (it.end <= it.start) continue;
    ev.push([it.start, 1], [it.end, -1]);
  }
  ev.sort((x, y) => x[0] - y[0] || x[1] - y[1]);
  let cur = 0;
  let best = 0;
  for (const [, d] of ev) {
    cur += d;
    if (cur > best) best = cur;
  }
  return best;
}

function validateResult(input, result) {
  assert.equal(result.ok, true);
  const byId = new Map(input.map((iv) => [iv.id, iv]));
  for (const iv of input) {
    const r = result.assignments[iv.id];
    assert.ok(Number.isInteger(r) && r >= 0, 'resource assigned for ' + iv.id);
    if (iv.resource !== undefined) {
      assert.equal(r, iv.resource, 'fixed binding preserved for ' + iv.id);
    }
  }
  const list = input.map((iv) => ({ ...iv, res: result.assignments[iv.id] }));
  for (let i = 0; i < list.length; i++) {
    for (let j = i + 1; j < list.length; j++) {
      if (ov(list[i], list[j])) {
        assert.notEqual(
          list[i].res,
          list[j].res,
          'overlapping ' + list[i].id + '/' + list[j].id + ' share a resource'
        );
      }
    }
  }
  const seen = new Map();
  for (const rs of result.schedule) {
    const starts = rs.intervals.map((id) => byId.get(id).start);
    for (let i = 1; i < starts.length; i++) {
      assert.ok(starts[i] >= starts[i - 1], 'schedule sorted by start');
    }
    for (const id of rs.intervals) seen.set(id, rs.resource);
  }
  for (const iv of input) {
    assert.equal(seen.get(iv.id), result.assignments[iv.id], 'schedule matches assignments');
  }
  assert.equal(result.resourceCount, result.schedule.length);
}

// 独立的暴力最优解:K 从下限递增,枚举自由区间的颜色分配。
function bruteOptimal(items) {
  const n = items.length;
  const adj = Array.from({ length: n }, () => []);
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (ov(items[i], items[j])) {
        adj[i].push(j);
        adj[j].push(i);
      }
    }
  }
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (items[i].pin >= 0 && items[i].pin === items[j].pin && ov(items[i], items[j])) {
        return null;
      }
    }
  }
  const pins = new Set(items.map((it) => it.pin).filter((p) => p >= 0));
  const free = [];
  for (let i = 0; i < n; i++) if (items[i].pin < 0) free.push(i);
  const lb = Math.max(pins.size, maxOverlapOf(items));
  for (let K = lb; K <= n; K++) {
    const palette = [...pins];
    for (let x = 0; palette.length < K; x++) {
      if (!pins.has(x)) palette.push(x);
    }
    const assign = new Array(n).fill(-1);
    const place = (pos) => {
      if (pos === free.length) return true;
      const v = free[pos];
      for (const c of palette) {
        let good = true;
        for (const u of adj[v]) {
          const cu = items[u].pin >= 0 ? items[u].pin : assign[u];
          if (cu === c) {
            good = false;
            break;
          }
        }
        if (good) {
          assign[v] = c;
          if (place(pos + 1)) return true;
          assign[v] = -1;
        }
      }
      return false;
    };
    if (place(0)) return K;
  }
  return null;
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('empty input', () => {
  const r = allocateResources([]);
  assert.equal(r.ok, true);
  assert.equal(r.resourceCount, 0);
  assert.deepEqual(r.schedule, []);
});

test('half-open boundary: touching intervals share a resource', () => {
  const r = allocateResources([
    { id: 'a', start: 0, end: 2 },
    { id: 'b', start: 2, end: 4 },
    { id: 'c', start: 1, end: 3 },
  ]);
  assert.equal(r.ok, true);
  assert.equal(r.resourceCount, 2);
  assert.equal(r.assignments.a, r.assignments.b);
  assert.notEqual(r.assignments.a, r.assignments.c);
  validateResult(
    [
      { id: 'a', start: 0, end: 2 },
      { id: 'b', start: 2, end: 4 },
      { id: 'c', start: 1, end: 3 },
    ],
    r
  );
});

test('fixed-binding conflicts are reported as pairs', () => {
  const r = allocateResources([
    { id: 'a', start: 0, end: 3, resource: 1 },
    { id: 'b', start: 2, end: 5, resource: 1 },
    { id: 'c', start: 4, end: 6, resource: 1 },
    { id: 'd', start: 0, end: 9 },
  ]);
  assert.equal(r.ok, false);
  assert.deepEqual(
    r.conflicts.map((p) => [p.resource, p.a, p.b]),
    [
      [1, 'a', 'b'],
      [1, 'b', 'c'],
    ]
  );
});

test('precoloring can force more resources than max overlap', () => {
  const input = [
    { id: 'v1', start: 0, end: 2, resource: 0 },
    { id: 'v2', start: 1, end: 3 },
    { id: 'v3', start: 2, end: 4 },
    { id: 'v4', start: 3, end: 5, resource: 0 },
  ];
  assert.equal(maxOverlapOf(input), 2);
  const r = allocateResources(input);
  assert.equal(r.ok, true);
  assert.equal(r.exact, true);
  assert.equal(r.resourceCount, 3);
  validateResult(input, r);
});

test('precoloring extension that stays within max overlap', () => {
  const input = [
    { id: 'v1', start: 0, end: 2, resource: 0 },
    { id: 'v2', start: 1, end: 3 },
    { id: 'v3', start: 2, end: 4 },
    { id: 'v4', start: 3, end: 5, resource: 1 },
  ];
  const r = allocateResources(input);
  assert.equal(r.ok, true);
  assert.equal(r.resourceCount, 2);
  validateResult(input, r);
});

test('zero-length intervals conflict with nothing', () => {
  const input = [
    { id: 'z', start: 3, end: 3 },
    { id: 'a', start: 0, end: 5 },
    { id: 'b', start: 3, end: 8 },
  ];
  const r = allocateResources(input);
  assert.equal(r.ok, true);
  assert.equal(r.resourceCount, 2);
  assert.equal(r.assignments.z, r.assignments.a);
  validateResult(input, r);
});

test('pinned zero-length interval cannot conflict', () => {
  const r = allocateResources([
    { id: 'z', start: 2, end: 2, resource: 4 },
    { id: 'a', start: 0, end: 9, resource: 4 },
    { id: 'z2', start: 2, end: 2, resource: 4 },
  ]);
  assert.equal(r.ok, true);
  assert.equal(r.resourceCount, 1);
});

test('holes in resource numbering are filled before extending', () => {
  const input = [
    { id: 'a', start: 0, end: 5, resource: 3 },
    { id: 'b', start: 5, end: 10, resource: 7 },
    { id: 'c', start: 0, end: 10 },
    { id: 'd', start: 2, end: 8 },
  ];
  const r = allocateResources(input);
  assert.equal(r.ok, true);
  assert.equal(r.assignments.a, 3);
  assert.equal(r.assignments.b, 7);
  assert.equal(r.assignments.c, 0);
  assert.equal(r.assignments.d, 1);
  assert.equal(r.resourceCount, 4);
  validateResult(input, r);
});

test('all intervals fixed', () => {
  const r = allocateResources([
    { id: 'a', start: 0, end: 2, resource: 5 },
    { id: 'b', start: 2, end: 4, resource: 5 },
  ]);
  assert.equal(r.ok, true);
  assert.equal(r.resourceCount, 1);
  assert.deepEqual(r.schedule, [{ resource: 5, intervals: ['a', 'b'] }]);
});

test('per-resource timetable is grouped and sorted', () => {
  const input = [
    { id: 'a', start: 0, end: 2 },
    { id: 'b', start: 1, end: 3 },
    { id: 'c', start: 2, end: 4 },
    { id: 'd', start: 3, end: 5 },
  ];
  const r = allocateResources(input);
  assert.equal(r.resourceCount, 2);
  for (const rs of r.schedule) {
    assert.ok(rs.intervals.length > 0);
  }
  validateResult(input, r);
});

test('invalid inputs throw', () => {
  assert.throws(() => allocateResources([{ id: 'x', start: 5, end: 2 }]));
  assert.throws(() =>
    allocateResources([
      { id: 'x', start: 0, end: 1 },
      { id: 'x', start: 0, end: 1 },
    ])
  );
  assert.throws(() => allocateResources([{ id: 'x', start: 0, end: 1, resource: -1 }]));
  assert.throws(() => allocateResources([{ id: 'x', start: 0, end: 1, resource: 1.5 }]));
  assert.throws(() => allocateResources([{ id: '', start: 0, end: 1 }]));
});

test('exactThreshold=0 forces the greedy path', () => {
  const r = allocateResources(
    [
      { id: 'a', start: 0, end: 2 },
      { id: 'b', start: 1, end: 3 },
    ],
    { exactThreshold: 0 }
  );
  assert.equal(r.ok, true);
  assert.equal(r.exact, false);
  assert.equal(r.resourceCount, 2);
});

test('large instance uses greedy and stays valid', () => {
  const rand = mulberry32(999);
  const input = [];
  for (let i = 0; i < 80; i++) {
    const s = Math.floor(rand() * 100);
    const len = 1 + Math.floor(rand() * 20);
    input.push({ id: 'j' + i, start: s, end: s + len });
  }
  const r = allocateResources(input);
  assert.equal(r.ok, true);
  assert.equal(r.exact, false);
  validateResult(input, r);
  assert.equal(r.resourceCount, maxOverlapOf(input));
});

test('random small instances match brute-force optimum', () => {
  const rand = mulberry32(12345);
  for (let iter = 0; iter < 500; iter++) {
    const n = 1 + Math.floor(rand() * 9);
    const input = [];
    for (let i = 0; i < n; i++) {
      const s = Math.floor(rand() * 10);
      const len = Math.floor(rand() * 5);
      const iv = { id: 'i' + i, start: s, end: s + len };
      if (rand() < 0.35) iv.resource = Math.floor(rand() * 4);
      input.push(iv);
    }
    const result = allocateResources(input);
    const brute = bruteOptimal(
      input.map((iv) => ({
        start: iv.start,
        end: iv.end,
        pin: iv.resource === undefined ? -1 : iv.resource,
      }))
    );
    if (brute === null) {
      assert.equal(result.ok, false, 'infeasible detected at iter ' + iter);
      assert.ok(result.conflicts.length > 0);
    } else {
      assert.equal(result.ok, true, 'feasible at iter ' + iter);
      validateResult(input, result);
      assert.equal(
        result.resourceCount,
        brute,
        'optimal resource count at iter ' + iter + ' input=' + JSON.stringify(input)
      );
      assert.equal(result.exact, true);
    }
  }
});

console.log('');
if (failures.length > 0) {
  console.log(passed + ' passed, ' + failures.length + ' failed');
  for (const [name, err] of failures) {
    console.log('--- ' + name);
    console.log(err && err.stack ? err.stack : err);
  }
  process.exit(1);
} else {
  console.log('all ' + passed + ' tests passed');
}
