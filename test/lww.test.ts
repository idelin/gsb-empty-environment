import { JsonValue, LwwDocument, Operation, Snapshot } from '../src';

let failures = 0;
let checks = 0;

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a as object);
  const keysB = Object.keys(b as object);
  if (keysA.length !== keysB.length) return false;
  return keysA.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(b, key) &&
      deepEqual((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key]),
  );
}

function check(condition: boolean, message: string): void {
  checks++;
  if (!condition) {
    failures++;
    console.error(`FAIL: ${message}`);
  }
}

function checkValue(doc: LwwDocument, expected: JsonValue | undefined, message: string): void {
  check(deepEqual(doc.value(), expected), `${message} (got ${JSON.stringify(doc.value())})`);
}

function permutations<T>(items: T[]): T[][] {
  if (items.length <= 1) return [items];
  const result: T[][] = [];
  for (let i = 0; i < items.length; i++) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const perm of permutations(rest)) {
      result.push([items[i], ...perm]);
    }
  }
  return result;
}

function applyAll(doc: LwwDocument, ops: Operation[]): LwwDocument {
  for (const op of ops) doc.apply(op);
  return doc;
}

function checkPermutations(
  name: string,
  ops: Operation[],
  expected: JsonValue | undefined,
): void {
  const perms = permutations(ops);
  for (const perm of perms) {
    const doc = applyAll(LwwDocument.create(), perm);
    checkValue(doc, expected, `${name} order=${JSON.stringify(perm.map((op) => op.kind + ':' + op.path.join('.') + '@' + op.version + op.clientId))}`);
  }
  console.log(`ok: ${name} (${perms.length} permutations)`);
}

// 1. Tombstone blocks stale resurrection; independent fields merge.
checkPermutations(
  'tombstone blocks resurrection, independent fields merge',
  [
    { kind: 'set', path: ['user', 'name'], value: 'alice', version: 1, clientId: 'c1' },
    { kind: 'set', path: ['user', 'age'], value: 30, version: 1, clientId: 'c2' },
    { kind: 'set', path: ['user', 'name'], value: 'bob', version: 2, clientId: 'c1' },
    { kind: 'delete', path: ['user', 'age'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['user', 'age'], value: 25, version: 1, clientId: 'c3' },
  ],
  { user: { name: 'bob' } },
);

// 2. Same version: client id breaks the tie, order-independent.
checkPermutations(
  'client id tie-break on equal version',
  [
    { kind: 'set', path: ['x'], value: 'from-c1', version: 1, clientId: 'c1' },
    { kind: 'set', path: ['x'], value: 'from-c2', version: 1, clientId: 'c2' },
    { kind: 'set', path: ['y'], value: 1, version: 1, clientId: 'c1' },
  ],
  { x: 'from-c2', y: 1 },
);

// 3. Arrays are replaced wholesale, never merged element-wise.
checkPermutations(
  'array replaced wholesale',
  [
    { kind: 'set', path: ['tags'], value: ['a', 'b', 'c'], version: 1, clientId: 'c1' },
    { kind: 'set', path: ['tags'], value: ['z'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['tags', 'ignored'], value: true, version: 1, clientId: 'c3' },
  ],
  { tags: ['z'] },
);

// 4. Object updates decompose into leaf fields: untouched leaves survive.
checkPermutations(
  'object updates merge by leaf field',
  [
    { kind: 'set', path: ['user'], value: { name: 'a', age: 1 }, version: 1, clientId: 'c1' },
    { kind: 'set', path: ['user'], value: { name: 'b' }, version: 2, clientId: 'c2' },
    { kind: 'set', path: ['user', 'age'], value: 2, version: 2, clientId: 'c1' },
  ],
  { user: { name: 'b', age: 2 } },
);

// 5. Delete vs set at the same path: higher stamp wins either way.
checkPermutations(
  'newer set revives after delete',
  [
    { kind: 'set', path: ['x'], value: 1, version: 1, clientId: 'c1' },
    { kind: 'delete', path: ['x'], version: 2, clientId: 'c1' },
    { kind: 'set', path: ['x'], value: 2, version: 3, clientId: 'c2' },
  ],
  { x: 2 },
);

checkPermutations(
  'stale set cannot revive after delete',
  [
    { kind: 'set', path: ['x'], value: 1, version: 1, clientId: 'c1' },
    { kind: 'delete', path: ['x'], version: 2, clientId: 'c1' },
    { kind: 'set', path: ['x'], value: 2, version: 1, clientId: 'c2' },
  ],
  undefined,
);

// 6. Ancestor/descendant conflicts resolve by stamp, convergently.
checkPermutations(
  'scalar ancestor vs object descendant',
  [
    { kind: 'set', path: ['a'], value: 5, version: 1, clientId: 'c1' },
    { kind: 'set', path: ['a', 'b'], value: 3, version: 2, clientId: 'c2' },
  ],
  { a: { b: 3 } },
);

checkPermutations(
  'object descendant vs newer scalar ancestor',
  [
    { kind: 'set', path: ['a', 'b'], value: 3, version: 1, clientId: 'c2' },
    { kind: 'set', path: ['a'], value: 5, version: 2, clientId: 'c1' },
  ],
  { a: 5 },
);

checkPermutations(
  'delete of parent hides stale children',
  [
    { kind: 'set', path: ['a'], value: { b: 1, c: 2 }, version: 1, clientId: 'c1' },
    { kind: 'delete', path: ['a'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['a', 'b'], value: 9, version: 1, clientId: 'c3' },
  ],
  undefined,
);

checkPermutations(
  'empty object set clears children',
  [
    { kind: 'set', path: ['user'], value: { name: 'a', age: 1 }, version: 1, clientId: 'c1' },
    { kind: 'set', path: ['user'], value: {}, version: 2, clientId: 'c2' },
  ],
  { user: {} },
);

// 7. Restore (snapshot round-trip) converges for every permutation split.
{
  const ops: Operation[] = [
    { kind: 'set', path: ['user', 'name'], value: 'alice', version: 1, clientId: 'c1' },
    { kind: 'set', path: ['user', 'age'], value: 30, version: 1, clientId: 'c2' },
    { kind: 'delete', path: ['user', 'age'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['user', 'name'], value: 'bob', version: 2, clientId: 'c1' },
    { kind: 'set', path: ['user', 'age'], value: 25, version: 1, clientId: 'c3' },
  ];
  const expected = { user: { name: 'bob' } };
  const perms = permutations(ops);
  for (const perm of perms) {
    for (let split = 0; split <= perm.length; split++) {
      const live = applyAll(LwwDocument.create(), perm.slice(0, split));
      const restored = LwwDocument.restore(JSON.parse(JSON.stringify(live.snapshot())) as Snapshot);
      applyAll(restored, perm.slice(split));
      checkValue(restored, expected, `restore converges split=${split}`);
    }
  }
  console.log(`ok: restore converges (${perms.length * (ops.length + 1)} splits)`);
}

// 8. Message replay (duplicates) is idempotent.
{
  const ops: Operation[] = [
    { kind: 'set', path: ['a'], value: 1, version: 1, clientId: 'c1' },
    { kind: 'delete', path: ['a'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['b'], value: [1, 2], version: 1, clientId: 'c1' },
  ];
  const doc = applyAll(LwwDocument.create(), ops);
  const before = doc.value();
  applyAll(doc, ops);
  applyAll(doc, [...ops].reverse());
  check(deepEqual(doc.value(), before), 'replaying messages is idempotent');
  checkValue(doc, { b: [1, 2] }, 'replay keeps tombstone effect');
  console.log('ok: replay idempotent');
}

// 9. Two replicas applying ops in different orders converge via merge.
{
  const ops: Operation[] = [
    { kind: 'set', path: ['user', 'name'], value: 'alice', version: 1, clientId: 'c1' },
    { kind: 'set', path: ['user', 'age'], value: 30, version: 1, clientId: 'c2' },
    { kind: 'delete', path: ['user', 'age'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['user', 'name'], value: 'bob', version: 2, clientId: 'c1' },
    { kind: 'set', path: ['tags'], value: ['x'], version: 1, clientId: 'c3' },
  ];
  const expected = { user: { name: 'bob' }, tags: ['x'] };
  for (const perm of permutations(ops)) {
    const replicaA = applyAll(LwwDocument.create(), perm);
    const replicaB = applyAll(LwwDocument.create(), [...perm].reverse());
    replicaA.merge(replicaB.snapshot());
    replicaB.merge(replicaA.snapshot());
    checkValue(replicaA, expected, 'replica A converges after merge');
    checkValue(replicaB, expected, 'replica B converges after merge');
    check(
      JSON.stringify(replicaA.snapshot()) === JSON.stringify(replicaB.snapshot()),
      'snapshots identical after merge',
    );
  }
  console.log('ok: replicas converge via merge (120 orderings)');
}

console.log(`${checks} checks, ${failures} failures`);
if (failures > 0) {
  process.exit(1);
}
