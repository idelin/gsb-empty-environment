// Finite event exploration.
//
// A seeded random generator emits events (single writes, multi-write
// transactions with repeated writes to the same state, flag toggles that
// dynamically form and break dependency cycles, and re-entrant writes from
// inside computations). After EVERY event the incremental engine is compared
// against the independent full-recompute model: every computed value/error
// (including cycle chains), every effect output, and every commit error must
// match. The test also counts how many node recomputations each
// implementation actually performed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  describeError,
  FnRegistry,
  ReactiveSystem,
  type NodeValue,
  type StateHandle,
} from '../src/core.ts';
import { ModelSystem } from '../src/model.ts';

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

const STATES = ['s0', 's1', 's2', 's3'] as const;

function buildRegistry(): FnRegistry {
  const registry = new FnRegistry();
  // Diamond: join reads left+right, both read s0.
  registry.register('left', (cx) => (cx.get('s0') as number) + 1);
  registry.register('right', (cx) => (cx.get('s0') as number) + 2);
  registry.register('join', (cx) => (cx.get('left') as number) + (cx.get('right') as number));
  // Dynamic branch: dependencies change with the flag parity of s2.
  registry.register('branch', (cx) =>
    (cx.get('s2') as number) % 2 === 0 ? cx.get('s0') : cx.get('s1'),
  );
  // Conditional cycle pair: a cycle exists exactly when s3 is odd (ca->cb)
  // and s2 is odd (cb->ca). Toggling flags forms and breaks the cycle.
  registry.register('ca', (cx) =>
    (cx.get('s3') as number) % 2 === 1 ? cx.get('cb') : cx.get('s0'),
  );
  registry.register('cb', (cx) =>
    (cx.get('s2') as number) % 2 === 1 ? cx.get('ca') : cx.get('s1'),
  );
  // Sink mixing everything (propagates cycle errors when they occur).
  registry.register('sink', (cx) => {
    const j = cx.get('join') as number;
    const b = cx.get('branch') as number;
    const a = cx.get('ca') as number;
    return j + b + a;
  });
  // Re-entrant write: converges s1 up to s0, deferred to the next round.
  registry.register('bump', (cx) => {
    const v = cx.get('s0') as number;
    if ((cx.get('s1') as number) < v) cx.set('s1', v);
    return v;
  });
  // Effects.
  registry.register('eJoin', (cx) => [cx.get('join'), cx.get('branch')]);
  registry.register('eCycle', (cx) => [cx.get('ca'), cx.get('cb')]);
  registry.register('eBump', (cx) => [cx.get('s0'), cx.get('s1'), cx.get('bump')]);
  return registry;
}

interface Engine {
  sys: ReactiveSystem;
  states: Record<string, StateHandle<number>>;
}

function buildEngine(registry: FnRegistry): Engine {
  const sys = new ReactiveSystem(registry);
  const states: Record<string, StateHandle<number>> = {};
  for (const name of STATES) states[name] = sys.state(name, 0);
  sys.computed('left', 'left');
  sys.computed('right', 'right');
  sys.computed('join', 'join');
  sys.computed('branch', 'branch');
  sys.computed('ca', 'ca');
  sys.computed('cb', 'cb');
  sys.computed('sink', 'sink');
  sys.computed('bump', 'bump');
  sys.effect('eJoin', 'eJoin');
  sys.effect('eCycle', 'eCycle');
  sys.effect('eBump', 'eBump');
  return { sys, states };
}

function buildModel(registry: FnRegistry): ModelSystem {
  const model = new ModelSystem(registry);
  model.state('s0', 0);
  model.state('s1', 0);
  model.state('s2', 0);
  model.state('s3', 0);
  model.computed('left', 'left');
  model.computed('right', 'right');
  model.computed('join', 'join');
  model.computed('branch', 'branch');
  model.computed('ca', 'ca');
  model.computed('cb', 'cb');
  model.computed('sink', 'sink');
  model.computed('bump', 'bump');
  model.effect('eJoin', 'eJoin');
  model.effect('eCycle', 'eCycle');
  model.effect('eBump', 'eBump');
  return model;
}

function canonical(snapshot: Record<string, NodeValue>): Record<string, NodeValue> {
  // Canonicalize cycle chains by rotation so equivalent reports compare equal
  // regardless of which node the evaluator happened to enter first.
  const out: Record<string, NodeValue> = {};
  for (const [name, value] of Object.entries(snapshot)) {
    if (value.status === 'error' && value.chain) {
      const body = value.chain.slice(0, -1);
      let best = body;
      for (let i = 1; i < body.length; i++) {
        const rotated = [...body.slice(i), ...body.slice(0, i)];
        if (rotated.join('') < best.join('')) best = rotated;
      }
      out[name] = { status: 'error', message: 'cycle', chain: best };
    } else if (value.status === 'error') {
      out[name] = { status: 'error', message: value.message };
    } else {
      out[name] = value;
    }
  }
  return out;
}

function canonicalError(error: unknown): { message: string; chain?: string[] } {
  const described = describeError(error);
  if (!described.chain) return { message: described.message };
  const body = described.chain.slice(0, -1);
  let best = body;
  for (let i = 1; i < body.length; i++) {
    const rotated = [...body.slice(i), ...body.slice(0, i)];
    if (rotated.join('') < best.join('')) best = rotated;
  }
  return { message: 'cycle', chain: best };
}

function runCount(stats: Record<string, number>): number {
  return Object.values(stats).reduce((a, b) => a + b, 0);
}

test('finite event exploration: incremental engine == full-recompute model', (t) => {
  const SEEDS = 24;
  const EVENTS = 150;

  let engineRuns = 0;
  let modelRuns = 0;
  let cycleSnapshots = 0;
  let txEvents = 0;
  let deferredRounds = 0;

  for (let seed = 1; seed <= SEEDS; seed++) {
    const rng = mulberry32(seed);
    const registry = buildRegistry();
    const { sys: engine, states } = buildEngine(registry);
    const model = buildModel(registry);

    const check = (label: string): void => {
      const es = canonical(engine.snapshot());
      const ms = canonical(model.snapshot());
      assert.deepEqual(es, ms, `seed=${seed} ${label}: snapshot divergence`);
      for (const effect of ['eJoin', 'eCycle', 'eBump']) {
        const ei = engine.effectInfo(effect);
        const mi = model.effectInfo(effect);
        assert.deepEqual(
          ei.lastOutput,
          mi.lastOutput,
          `seed=${seed} ${label}: effect ${effect} output divergence`,
        );
        const ee = ei.error === undefined ? undefined : canonicalError(ei.error);
        const me = mi.error === undefined ? undefined : canonicalError(mi.error);
        assert.deepEqual(ee, me, `seed=${seed} ${label}: effect ${effect} error divergence`);
      }
    };

    check('initial');

    for (let event = 0; event < EVENTS; event++) {
      const roll = rng();
      const pick = (): string => STATES[Math.floor(rng() * STATES.length)]!;
      const value = (): number => Math.floor(rng() * 6);

      if (roll < 0.55) {
        const name = pick();
        const v = value();
        states[name]!.set(v);
        model.set(name, v);
        check(`set ${name}=${v}`);
      } else if (roll < 0.85) {
        // Transaction with 2-4 writes, possibly repeating the same state.
        const writes: [string, number][] = [];
        const n = 2 + Math.floor(rng() * 3);
        for (let i = 0; i < n; i++) writes.push([pick(), value()]);
        txEvents++;
        engine.tx(() => {
          for (const [name, v] of writes) states[name]!.set(v);
        });
        model.tx(() => {
          for (const [name, v] of writes) model.set(name, v);
        });
        check(`tx ${JSON.stringify(writes)}`);
      } else {
        // Flag toggle aimed at forming/breaking the conditional cycle.
        const name = rng() < 0.5 ? 's2' : 's3';
        const current = engine.snapshot()[name]!;
        const cur = current.status === 'ok' ? (current.value as number) : 0;
        const v = cur + 1;
        states[name]!.set(v);
        model.set(name, v);
        check(`toggle ${name}=${v}`);
      }

      const snap = engine.snapshot();
      if (Object.values(snap).some((v) => v.status === 'error' && v.chain)) cycleSnapshots++;
      if (engine.effectInfo('eBump').runs > 0) deferredRounds++;
    }

    engineRuns += runCount(engine.stats());
    modelRuns += runCount(model.stats());
  }

  t.diagnostic(`events: ${SEEDS} seeds x ${EVENTS} events (${txEvents} transactions)`);
  t.diagnostic(`snapshots containing a detected cycle: ${cycleSnapshots}`);
  t.diagnostic(`events after which bump effect had run: ${deferredRounds}`);
  t.diagnostic(`actual node recomputations — engine: ${engineRuns}, model: ${modelRuns}`);

  assert.ok(cycleSnapshots > 0, 'exploration must actually exercise cycles');
  assert.ok(txEvents > 0, 'exploration must actually exercise transactions');
  assert.ok(
    engineRuns < modelRuns,
    `incremental engine (${engineRuns}) must recompute less than the full model (${modelRuns})`,
  );
});
