import { Engine } from './engine.ts';
import { resolveReference } from './reference.ts';
import { pretty, resolutionKey } from './common.ts';
import type { ImportEdge, ModuleSpec, ReexportEdge, Resolution } from './types.ts';

let failures = 0;
let checks = 0;

function fail(message: string): void {
  failures += 1;
  console.error(`FAIL: ${message}`);
}

function crossCheck(
  eng: Engine,
  version: number,
  ids: string[],
  names: string[],
  ctx: string,
): void {
  const modules = eng.modulesAt(version);
  for (const m of ids) {
    for (const n of names) {
      checks += 1;
      const actual = eng.resolve(m, n, version);
      const expected = resolveReference(modules, { module: m, name: n });
      if (resolutionKey(actual) !== resolutionKey(expected)) {
        fail(
          `${ctx}: mismatch at ${m}.${n} @v${version}\n` +
            `  engine:    ${pretty(resolutionKey(actual))}\n` +
            `  reference: ${pretty(resolutionKey(expected))}`,
        );
      }
    }
  }
}

function expectKind(res: Resolution, kind: Resolution['kind'], ctx: string): void {
  checks += 1;
  if (res.kind !== kind) {
    fail(`${ctx}: expected kind ${kind}, got ${pretty(resolutionKey(res))}`);
  }
}

function expectDefined(
  res: Resolution,
  module: string,
  name: string,
  ctx: string,
): void {
  checks += 1;
  if (res.kind !== 'defined' || res.target.module !== module || res.target.name !== name) {
    fail(`${ctx}: expected defined at ${module}.${name}, got ${pretty(resolutionKey(res))}`);
  }
}

function expectConflict(
  res: Resolution,
  first: [string, string],
  second: [string, string],
  ctx: string,
): void {
  checks += 1;
  if (res.kind !== 'conflict') {
    fail(`${ctx}: expected conflict, got ${pretty(resolutionKey(res))}`);
    return;
  }
  const [a, b] = res.chains;
  const ok =
    a.target.module === first[0] && a.target.name === first[1] &&
    b.target.module === second[0] && b.target.name === second[1];
  if (!ok) {
    fail(
      `${ctx}: expected conflict chains to ${first} and ${second}, got ${pretty(resolutionKey(res))}`,
    );
  }
}

const spec = (
  definitions: string[] = [],
  imports: ImportEdge[] = [],
  reexports: ReexportEdge[] = [],
): ModuleSpec => ({ definitions, imports, reexports });

function directed(): void {
  const eng = new Engine();
  const ids = ['A', 'B', 'C', 'D', 'ghost'];
  const names = ['a', 'b', 'c', 'x', 'y', 'ghost'];
  const versions: number[] = [0];

  const step = (id: string, s: ModuleSpec | null, note: string): void => {
    const { version } = eng.update(id, s);
    versions.push(version);
    for (const v of versions) {
      crossCheck(eng, v, ids, names, `directed[${note}]`);
    }
  };

  // Diamond: B and C both re-export x from D; A imports x from both.
  step('D', spec(['x']), 'D defines x');
  step('B', spec([], [], [{ exported: 'x', from: 'D', imported: 'x' }]), 'B re-exports D.x');
  step('C', spec([], [], [{ exported: 'x', from: 'D', imported: 'x' }]), 'C re-exports D.x');
  step(
    'A',
    spec([], [
      { local: 'x', from: 'B', imported: 'x' },
      { local: 'x', from: 'C', imported: 'x' },
    ]),
    'A imports x from B and C',
  );
  expectDefined(eng.resolve('A', 'x'), 'D', 'x', 'diamond converges to one definition');
  const vDiamond = eng.version;

  // Deleting the final definition makes the whole diamond undefined.
  step('D', spec([]), 'D drops definition of x');
  expectKind(eng.resolve('A', 'x'), 'undefined', 'definition deleted');
  expectKind(eng.resolve('B', 'x'), 'undefined', 'definition deleted (B)');
  // The old snapshot still resolves.
  expectDefined(eng.resolve('A', 'x', vDiamond), 'D', 'x', 'old snapshot still resolves');

  // Restore, then switch to a rename chain: D.a -> C.b -> B.c -> A.x.
  step('D', spec(['a']), 'D defines a instead');
  step('C', spec([], [], [{ exported: 'b', from: 'D', imported: 'a' }]), 'C re-exports a as b');
  step('B', spec([], [], [{ exported: 'c', from: 'C', imported: 'b' }]), 'B re-exports b as c');
  step('A', spec([], [{ local: 'x', from: 'B', imported: 'c' }]), 'A imports c as x');
  const renamed = eng.resolve('A', 'x');
  expectDefined(renamed, 'D', 'a', 'rename chain resolves to D.a');
  if (renamed.kind === 'defined' && renamed.chain.edges.length !== 3) {
    fail(`rename chain should have 3 hops, got ${pretty(resolutionKey(renamed))}`);
  }

  // Deleting the defining module, then recreating it.
  step('D', null, 'delete module D');
  expectKind(eng.resolve('A', 'x'), 'undefined', 'defining module deleted');
  step('D', spec(['a']), 'recreate D with a');
  expectDefined(eng.resolve('A', 'x'), 'D', 'a', 'resolution restored after recreate');

  // Conflict: A imports x from B and re-exports x from C; both define x.
  step('B', spec(['x']), 'B defines x');
  step('C', spec(['x']), 'C defines x');
  step(
    'A',
    spec([], [{ local: 'x', from: 'B', imported: 'x' }], [
      { exported: 'x', from: 'C', imported: 'x' },
    ]),
    'A has two sources for x',
  );
  expectConflict(eng.resolve('A', 'x'), ['B', 'x'], ['C', 'x'], 'conflict reports two chains');

  // Conflict with chains of different lengths: B.x direct, D.x two hops away.
  step('C', spec([], [], [{ exported: 'x', from: 'D', imported: 'x' }]), 'C re-exports D.x');
  step('D', spec(['x']), 'D defines x');
  const mixed = eng.resolve('A', 'x');
  expectConflict(mixed, ['B', 'x'], ['D', 'x'], 'two shortest chains, different lengths');
  if (mixed.kind === 'conflict') {
    const lengths = mixed.chains.map((c) => c.edges.length);
    if (lengths[0] !== 1 || lengths[1] !== 2) {
      fail(`expected chain lengths 1 and 2, got ${lengths}`);
    }
  }

  // Circular re-export without a definition is rejected.
  step('A', spec([], [], [{ exported: 'x', from: 'B', imported: 'x' }]), 'A re-exports B.x');
  step('B', spec([], [], [{ exported: 'x', from: 'A', imported: 'x' }]), 'B re-exports A.x');
  step('C', spec([]), 'C empty');
  step('D', spec([]), 'D empty');
  expectKind(eng.resolve('A', 'x'), 'cyclic-reexport', 'pure re-export cycle rejected');
  expectKind(eng.resolve('B', 'x'), 'cyclic-reexport', 'pure re-export cycle rejected (B)');

  // A definition shadows the re-export and breaks the cycle.
  step('B', spec(['x'], [], [{ exported: 'x', from: 'A', imported: 'x' }]), 'B defines x');
  expectDefined(eng.resolve('A', 'x'), 'B', 'x', 'definition breaks cycle');
  expectDefined(eng.resolve('B', 'x'), 'B', 'x', 'B.x defined locally');

  // Circular imports are allowed: no definition anywhere, no error.
  step('A', spec([], [{ local: 'x', from: 'B', imported: 'x' }]), 'A imports x from B');
  step('B', spec([], [{ local: 'x', from: 'A', imported: 'x' }]), 'B imports x from A');
  expectKind(eng.resolve('A', 'x'), 'undefined', 'circular import allowed');
  expectKind(eng.resolve('B', 'x'), 'undefined', 'circular import allowed (B)');

  // Once A defines x, B resolves through the import cycle.
  step('A', spec(['x'], [{ local: 'x', from: 'B', imported: 'x' }]), 'A defines x');
  expectDefined(eng.resolve('B', 'x'), 'A', 'x', 'import cycle resolves once defined');

  // Mixed cycle (re-export + import) without a definition: allowed, undefined.
  step('A', spec([], [], [{ exported: 'x', from: 'B', imported: 'x' }]), 'A re-exports B.x');
  expectKind(eng.resolve('A', 'x'), 'undefined', 'mixed cycle is not rejected');
  expectKind(eng.resolve('B', 'x'), 'undefined', 'mixed cycle is not rejected (B)');

  console.log(`directed scenarios done (${eng.version} versions)`);
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface MutableSpec {
  definitions: string[];
  imports: ImportEdge[];
  reexports: ReexportEdge[];
}

function fuzz(seed: number, ops: number): void {
  const rand = mulberry32(seed);
  const eng = new Engine();
  const live = ['m0', 'm1', 'm2', 'm3', 'm4', 'm5', 'm6', 'm7'];
  const ids = [...live, 'ghost'];
  const names = ['a', 'b', 'c', 'd', 'e', 'f', 'ghost'];
  const pick = <T>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)];
  const specs = new Map<string, MutableSpec>();
  const versions: number[] = [0];

  for (let i = 0; i < ops; i += 1) {
    const id = pick(live);
    let cur: MutableSpec = specs.get(id) ?? { definitions: [], imports: [], reexports: [] };
    const roll = rand();
    if (roll < 0.25) {
      const n = pick(names.slice(0, 6));
      cur = {
        ...cur,
        definitions: cur.definitions.includes(n)
          ? cur.definitions.filter((d) => d !== n)
          : [...cur.definitions, n],
      };
      specs.set(id, cur);
    } else if (roll < 0.45) {
      const edge: ImportEdge = {
        local: pick(names.slice(0, 6)),
        from: pick(live),
        imported: pick(names.slice(0, 6)),
      };
      cur = { ...cur, imports: [...cur.imports.filter((x) => x.local !== edge.local), edge] };
      specs.set(id, cur);
    } else if (roll < 0.55) {
      if (cur.imports.length > 0) {
        const victim = pick(cur.imports);
        cur = { ...cur, imports: cur.imports.filter((x) => x !== victim) };
        specs.set(id, cur);
      }
    } else if (roll < 0.75) {
      const edge: ReexportEdge = {
        exported: pick(names.slice(0, 6)),
        from: pick(live),
        imported: pick(names.slice(0, 6)),
      };
      cur = {
        ...cur,
        reexports: [...cur.reexports.filter((x) => x.exported !== edge.exported), edge],
      };
      specs.set(id, cur);
    } else if (roll < 0.85) {
      if (cur.reexports.length > 0) {
        const victim = pick(cur.reexports);
        cur = { ...cur, reexports: cur.reexports.filter((x) => x !== victim) };
        specs.set(id, cur);
      }
    } else if (roll < 0.92) {
      specs.delete(id);
    } else {
      cur = {
        definitions: rand() < 0.5 ? [pick(names.slice(0, 6))] : [],
        imports: [],
        reexports: [],
      };
      specs.set(id, cur);
    }

    const current = specs.get(id);
    const { version } = eng.update(
      id,
      current
        ? {
            definitions: [...current.definitions],
            imports: [...current.imports],
            reexports: [...current.reexports],
          }
        : null,
    );
    versions.push(version);
    crossCheck(eng, version, ids, names, `fuzz seed=${seed} op=${i}`);
    if ((i + 1) % 50 === 0) {
      for (const v of versions) {
        crossCheck(eng, v, ids, names, `fuzz seed=${seed} op=${i} history`);
      }
    }
  }
  for (const v of versions) {
    crossCheck(eng, v, ids, names, `fuzz seed=${seed} final history`);
  }
  console.log(`fuzz seed=${seed} done (${ops} ops, ${eng.version + 1} versions)`);
}

directed();
fuzz(1, 400);
fuzz(2, 400);
fuzz(3, 400);

console.log(`${checks} symbol resolutions cross-checked`);
if (failures > 0) {
  console.error(`${failures} FAILURES`);
  process.exit(1);
}
console.log('all checks passed');
