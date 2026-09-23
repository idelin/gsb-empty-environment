/**
 * Model-based verification.
 *
 * The incremental engine is checked against an independent oracle that
 * re-evaluates every computed from scratch (naive lazy evaluation with
 * cycle detection) after every event. We explore:
 *  - exhaustively, all event sequences up to length 3 over small domains;
 *  - randomly (seeded), longer sequences including multi-write batches.
 *
 * Checked invariants after every event:
 *  1. every computed's value/error matches the oracle;
 *  2. a reported cycle chain is a real cycle in the oracle's read graph;
 *  3. no computed recomputes more than once per event (necessity bound);
 *  4. the probe effect only ever observes oracle-consistent snapshots;
 *  5. replaying the operation log on a fresh world reproduces the oracle.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { World, CycleError } from '../src/reactive.ts';

type Read = (name: string) => unknown;
interface Spec {
  states: Record<string, unknown>;
  computeds: Record<string, (read: Read) => unknown>;
}
type Result = { ok: unknown } | { err: unknown };
interface CycleSentinel {
  cycle: string[];
}
type Event = { set: [string, unknown] } | { batch: Array<[string, unknown]> };

function isCycleSentinel(error: unknown): error is CycleSentinel {
  return typeof error === 'object' && error !== null && Array.isArray((error as CycleSentinel).cycle);
}

/** Independent model: full re-evaluation of every computed, every time. */
function oracle(spec: Spec, values: Record<string, unknown>): {
  results: Map<string, Result>;
  edges: Set<string>;
} {
  const memo = new Map<string, Result>();
  const visiting: string[] = [];
  const edges = new Set<string>();
  const evalNode = (name: string): Result => {
    const hit = memo.get(name);
    if (hit !== undefined) return hit;
    const idx = visiting.indexOf(name);
    if (idx >= 0) {
      const sentinel: CycleSentinel = { cycle: [...visiting.slice(idx), name] };
      throw sentinel;
    }
    visiting.push(name);
    let result: Result;
    try {
      const value = spec.computeds[name]((dep) => {
        edges.add(`${name}>${dep}`);
        if (dep in spec.computeds) {
          const r = evalNode(dep);
          if ('err' in r) throw r.err;
          return r.ok;
        }
        return values[dep];
      });
      result = { ok: value };
    } catch (error) {
      result = { err: error };
    }
    visiting.pop();
    memo.set(name, result);
    return result;
  };
  for (const name of Object.keys(spec.computeds)) {
    try {
      evalNode(name);
    } catch {
      // unreachable: evalNode captures errors into results
    }
  }
  return { results: memo, edges };
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

function resultKey(result: Result): string {
  if ('ok' in result) return `ok:${JSON.stringify(result.ok)}`;
  if (result.err instanceof CycleError || isCycleSentinel(result.err)) return 'err:cycle';
  const message = (result.err as Error)?.message ?? String(result.err);
  return `err:${message}`;
}

function resultsMatch(engine: Result, model: Result): boolean {
  return resultKey(engine) === resultKey(model);
}

interface Stats {
  events: number;
  recomputes: number;
  oracleEvals: number;
  recomputedNodes: Set<string>;
}

function buildWorld(spec: Spec): {
  world: World;
  sources: Record<string, { get(): unknown }>;
} {
  const world = new World();
  const sources: Record<string, { get(): unknown }> = {};
  for (const [name, initial] of Object.entries(spec.states)) {
    const cell = world.state(name, initial);
    sources[name] = cell;
  }
  for (const [name, fn] of Object.entries(spec.computeds)) {
    sources[name] = world.computed(name, () => fn((dep) => sources[dep].get()));
  }
  return { world, sources };
}

function readEngine(sources: Record<string, { get(): unknown }>, name: string): Result {
  try {
    return { ok: sources[name].get() };
  } catch (error) {
    return { err: error };
  }
}

function runSequence(spec: Spec, events: Event[], stats: Stats): void {
  const { world, sources } = buildWorld(spec);
  const names = Object.keys(spec.computeds);
  const values = { ...spec.states };

  const snapshots: string[] = [];
  world.effect('probe', () => {
    const snap: Record<string, string> = {};
    for (const name of names) snap[name] = resultKey(readEngine(sources, name));
    snapshots.push(JSON.stringify(snap));
  });

  const checkAll = (): void => {
    const model = oracle(spec, values);
    for (const name of names) {
      const engineResult = readEngine(sources, name);
      const modelResult = model.results.get(name);
      assert.ok(modelResult !== undefined);
      assert.ok(
        resultsMatch(engineResult, modelResult),
        `value mismatch at '${name}': engine=${resultKey(engineResult)} model=${resultKey(modelResult)}`,
      );
      if (engineResult.err instanceof CycleError) {
        const chain = engineResult.err.chain;
        assert.equal(chain[0], chain[chain.length - 1], 'chain must be a closed loop');
        for (let i = 0; i + 1 < chain.length; i++) {
          assert.ok(
            model.edges.has(`${chain[i]}>${chain[i + 1]}`),
            `chain edge ${chain[i]} -> ${chain[i + 1]} is not a real read edge`,
          );
        }
      }
    }
    const expectedSnap: Record<string, string> = {};
    for (const name of names) expectedSnap[name] = resultKey(model.results.get(name) as Result);
    assert.equal(
      snapshots[snapshots.length - 1],
      JSON.stringify(expectedSnap),
      'effect observed a non-model snapshot',
    );
  };

  checkAll();
  const counts = new Map(names.map((name) => [name, world.recomputeCount(name)]));
  for (const event of events) {
    const writes: Array<[string, unknown]> = 'set' in event ? [event.set] : event.batch;
    world.batch(() => {
      for (const [name, value] of writes) {
        (sources[name] as { set(v: unknown): void }).set(value);
        values[name] = value;
      }
    });
    stats.events++;
    checkAll();
    for (const name of names) {
      const count = world.recomputeCount(name);
      const delta = count - (counts.get(name) ?? 0);
      assert.ok(delta <= 1, `'${name}' recomputed ${delta} times within one event`);
      if (delta > 0) stats.recomputedNodes.add(`${specLabel(spec)}:${name}`);
      stats.recomputes += delta;
      counts.set(name, count);
    }
    stats.oracleEvals += names.length;
  }

  // Replay the recorded operation log on a fresh world.
  const replayed = buildWorld(spec);
  replayed.world.applyLog(world.log);
  const model = oracle(spec, values);
  for (const name of names) {
    const modelResult = model.results.get(name) as Result;
    assert.ok(
      resultsMatch(readEngine(replayed.sources, name), modelResult),
      `replay mismatch at '${name}'`,
    );
  }
}

const specLabels = new WeakMap<Spec, string>();
function specLabel(spec: Spec): string {
  return specLabels.get(spec) ?? 'spec';
}

const diamondSpec: Spec = {
  states: { s: 1, flag: true },
  computeds: {
    b: (read) => (read('s') as number) + 1,
    c: (read) => (read('s') as number) * 2,
    d: (read) => (read('b') as number) + (read('c') as number),
    sel: (read) => (read('flag') ? read('b') : read('c')),
    top: (read) => (read('d') as number) + (read('sel') as number),
  },
};

const cycleSpec: Spec = {
  states: { flag: true, x: 1 },
  computeds: {
    A: (read) => (read('flag') ? read('x') : (read('B') as number) + 1),
    B: (read) => (read('A') as number) * 2,
    C: (read) => (read('flag') ? read('B') : read('x')),
  },
};

const errorSpec: Spec = {
  states: { s: 1 },
  computeds: {
    t: (read) => {
      const v = read('s') as number;
      if (v < 0) throw new Error('neg');
      return v * 10;
    },
    u: (read) => (read('t') as number) + 1,
    v: (read) => (read('s') as number) % 2,
    w: (read) => (read('u') as number) + (read('v') as number),
  },
};

const SPECS: Array<[string, Spec, Record<string, unknown[]>]> = [
  ['diamond', diamondSpec, { s: [0, 1, 2, 3], flag: [true, false] }],
  ['cycle', cycleSpec, { flag: [true, false], x: [0, 1, 2] }],
  ['error', errorSpec, { s: [-1, 0, 1, 2] }],
];
for (const [label, spec] of SPECS) specLabels.set(spec, label);

function singleEvents(domains: Record<string, unknown[]>): Event[] {
  const events: Event[] = [];
  for (const [name, vals] of Object.entries(domains)) {
    for (const value of vals) events.push({ set: [name, value] });
  }
  return events;
}

function* sequences<T>(alphabet: T[], length: number, prefix: T[] = []): Generator<T[]> {
  if (length === 0) {
    yield prefix;
    return;
  }
  for (const item of alphabet) yield* sequences(alphabet, length - 1, [...prefix, item]);
}

test('model: exhaustive bounded event exploration (all sequences up to length 3)', () => {
  const stats: Stats = { events: 0, recomputes: 0, oracleEvals: 0, recomputedNodes: new Set() };
  for (const [, spec, domains] of SPECS) {
    const alphabet = singleEvents(domains);
    for (let len = 1; len <= 3; len++) {
      for (const seq of sequences(alphabet, len)) runSequence(spec, seq, stats);
    }
  }
  assert.ok(stats.recomputes <= stats.oracleEvals);
  console.log(
    `  [exhaustive] events=${stats.events} engineRecomputes=${stats.recomputes} ` +
      `oracleEvals=${stats.oracleEvals} distinctRecomputedNodes=${stats.recomputedNodes.size}`,
  );
});

test('model: seeded random exploration with batches (length 12, 150 sequences per spec)', () => {
  const stats: Stats = { events: 0, recomputes: 0, oracleEvals: 0, recomputedNodes: new Set() };
  for (const [, spec, domains] of SPECS) {
    const rand = mulberry32(0xc0ffee);
    const names = Object.keys(domains);
    for (let seq = 0; seq < 150; seq++) {
      const events: Event[] = [];
      for (let i = 0; i < 12; i++) {
        const pick = (): [string, unknown] => {
          const name = names[Math.floor(rand() * names.length)];
          const vals = domains[name];
          return [name, vals[Math.floor(rand() * vals.length)]];
        };
        if (rand() < 0.3) {
          const batch: Array<[string, unknown]> = [pick(), pick()];
          if (rand() < 0.5) batch.push(pick());
          events.push({ batch });
        } else {
          events.push({ set: pick() });
        }
      }
      runSequence(spec, events, stats);
    }
  }
  assert.ok(stats.recomputes <= stats.oracleEvals);
  console.log(
    `  [random] events=${stats.events} engineRecomputes=${stats.recomputes} ` +
      `oracleEvals=${stats.oracleEvals} distinctRecomputedNodes=${stats.recomputedNodes.size}`,
  );
});
