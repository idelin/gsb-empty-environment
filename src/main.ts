import { rmSync } from 'node:fs';
import { join } from 'node:path';
import { Simulator } from './simulator.ts';
import { MigState } from './types.ts';
import { digestOf } from './digest.ts';
import { mulberry32 } from './prng.ts';

const DATA_ROOT = 'data';

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`ASSERT FAILED: ${msg}`);
}

function hasTrace(sim: Simulator, needle: string): boolean {
  return sim.traceLog.some((l) => l.includes(needle));
}

interface Scenario {
  name: string;
  run(): void;
}

const scenarios: Scenario[] = [];

function scenario(name: string, run: () => void): void {
  scenarios.push({ name, run });
}

// 1. Happy path: upload -> receipt -> verify -> persist decision -> reclaim.
scenario('1. happy path: verified reclaim', () => {
  const sim = new Simulator({ seed: 1, dataDir: join(DATA_ROOT, 's1') });
  sim.writeLocal('obj-a', 'hello-v1');
  sim.run();
  const m = sim.migrators.get('obj-a')!;
  assert(m.state === MigState.Reclaimed, `expected RECLAIMED, got ${m.state}`);
  assert(!sim.local.has('obj-a'), 'local copy should be gone');
  assert(sim.remote.digest('obj-a') === digestOf('hello-v1'), 'remote must hold v1');
  assert(sim.decisions.has('obj-a', 1, digestOf('hello-v1')), 'decision must be persisted');
  assert(sim.violations.length === 0, 'no invariant violations');
});

// 2. Receipt lost: timeout -> probe target -> verify -> persist -> reclaim.
scenario('2. receipt loss: probe-based confirmation', () => {
  const sim = new Simulator({ seed: 1, dataDir: join(DATA_ROOT, 's2'), receiptLossProb: 1 });
  sim.writeLocal('obj-b', 'payload-v1');
  sim.run();
  const m = sim.migrators.get('obj-b')!;
  assert(hasTrace(sim, 'LOST in transit'), 'receipt should have been lost');
  assert(hasTrace(sim, 'receipt timeout'), 'should fall back to probing the target');
  assert(m.state === MigState.Reclaimed, `expected RECLAIMED, got ${m.state}`);
  assert(sim.violations.length === 0, 'no invariant violations');
});

// 3. Update race: v2 written mid-migration; the v1 receipt must be rejected.
scenario('3. update race: stale receipt rejected', () => {
  const sim = new Simulator({ seed: 1, dataDir: join(DATA_ROOT, 's3') });
  sim.writeLocal('obj-c', 'v1-bytes');
  sim.scheduleAt(6, 'update', () => sim.writeLocal('obj-c', 'v2-bytes-longer'));
  sim.run();
  const m = sim.migrators.get('obj-c')!;
  assert(hasTrace(sim, 'UPDATE RACE'), 'race should be detected');
  assert(hasTrace(sim, 'STALE receipt'), 'old receipt must be rejected');
  assert(m.state === MigState.Reclaimed, `expected RECLAIMED, got ${m.state}`);
  assert(sim.remote.digest('obj-c') === digestOf('v2-bytes-longer'), 'remote must hold v2');
  assert(sim.decisions.has('obj-c', 2, digestOf('v2-bytes-longer')), 'decision must be for v2');
  assert(sim.violations.length === 0, 'no invariant violations');
});

// 4. Target corruption before read-back: verify fails, re-upload, then reclaim.
scenario('4. target corruption: re-upload and recover', () => {
  const sim = new Simulator({ seed: 1, dataDir: join(DATA_ROOT, 's4') });
  sim.writeLocal('obj-d', 'precious-payload');
  sim.scheduleAt(8, 'corrupt', () => sim.corruptRemote('obj-d'));
  sim.run();
  const m = sim.migrators.get('obj-d')!;
  assert(hasTrace(sim, 'TARGET CORRUPTED'), 'corruption injected');
  assert(hasTrace(sim, 'digest mismatch'), 'read-back must catch the corruption');
  assert(m.state === MigState.Reclaimed, `expected RECLAIMED, got ${m.state}`);
  assert(sim.remote.digest('obj-d') === digestOf('precious-payload'), 'remote healed');
  assert(sim.violations.length === 0, 'no invariant violations');
});

// 5. Open read handle delays deletion until the handle is released.
scenario('5. open handle defers reclaim', () => {
  const sim = new Simulator({ seed: 1, dataDir: join(DATA_ROOT, 's5') });
  sim.writeLocal('obj-e', 'read-me');
  sim.scheduleAt(3, 'acquire', () => sim.acquireHandle('obj-e'));
  sim.scheduleAt(30, 'check-blocked', () => {
    const m = sim.migrators.get('obj-e')!;
    assert(m.state === MigState.ReclaimBlocked, `expected RECLAIM_BLOCKED, got ${m.state}`);
    assert(sim.local.has('obj-e'), 'local copy must survive while handles are open');
  });
  sim.scheduleAt(40, 'release', () => sim.releaseHandle('obj-e'));
  sim.run();
  const m = sim.migrators.get('obj-e')!;
  assert(hasTrace(sim, 'reclaim DEFERRED'), 'deletion should be deferred');
  assert(m.state === MigState.Reclaimed, `expected RECLAIMED, got ${m.state}`);
  assert(!sim.local.has('obj-e'), 'local copy reclaimed after handle release');
  assert(sim.violations.length === 0, 'no invariant violations');
});

// 6. Negative control: a naive policy (reclaim on receipt alone) loses data
//    when the target is corrupted after the receipt was issued.
scenario('6. naive policy loses data (negative control)', () => {
  const sim = new Simulator({ seed: 1, dataDir: join(DATA_ROOT, 's6'), unsafe: true });
  sim.writeLocal('obj-f', 'fragile');
  sim.scheduleAt(9, 'corrupt', () => sim.corruptRemote('obj-f'));
  sim.run();
  assert(sim.violations.length > 0, 'naive policy must violate the invariant');
});

// 7. Stress: 20 objects, random updates / corruption / receipt loss / handles,
//    swept over several seeds and fault rates.
scenario('7. stress: randomized faults, invariant holds', () => {
  for (const [seed, receiptLossProb] of [[42, 0.3], [7, 0.8], [99, 0.1], [2024, 0.5]] as const) {
    const sim = new Simulator({ seed, dataDir: join(DATA_ROOT, `s7-${seed}`), receiptLossProb });
    const rng = mulberry32(seed * 31 + 7);
    const ids = Array.from({ length: 20 }, (_, i) => `obj-${i}`);
    const pick = <T>(arr: T[]): T => arr[Math.floor(rng() * arr.length)];

    ids.forEach((id, i) => sim.scheduleAt(i, 'init', () => sim.writeLocal(id, `content-${id}-v1`)));
    for (let k = 0; k < 60; k++) {
      const t = 5 + Math.floor(rng() * 400);
      const id = pick(ids);
      sim.scheduleAt(t, 'update', () => sim.writeLocal(id, `content-${id}-upd-${k}`));
    }
    for (let k = 0; k < 40; k++) {
      const t = 5 + Math.floor(rng() * 400);
      const id = pick(ids);
      // Fault-model boundary: corruption is injected only during the
      // unverified migration window (upload / await receipt / verify), where
      // the state machine is responsible for detecting it via read-back.
      // Once the read-back has verified and the decision is being persisted,
      // ownership has handed off to the cold tier, whose own durability
      // (checksums, redundancy) is out of scope for this simulator.
      sim.scheduleAt(t, 'corrupt', () => {
        const m = sim.migrators.get(id);
        const unverified =
          m &&
          (m.state === MigState.Uploading ||
            m.state === MigState.AwaitingReceipt ||
            m.state === MigState.Verifying);
        if (unverified) sim.corruptRemote(id);
      });
    }
    for (let k = 0; k < 60; k++) {
      const t = Math.floor(rng() * 400);
      const id = pick(ids);
      sim.scheduleAt(t, 'acquire', () => {
        if (sim.acquireHandle(id)) {
          sim.schedule(1 + Math.floor(rng() * 20), 'release', () => sim.releaseHandle(id));
        }
      });
    }
    sim.run();
    const reclaimed = [...sim.migrators.values()].filter((m) => m.state === MigState.Reclaimed).length;
    console.log(
      `    seed=${seed} receiptLoss=${receiptLossProb} objects=${ids.length} ` +
        `reclaimed=${reclaimed} decisions=${sim.decisions.size} events=${sim.traceLog.length}`,
    );
    assert(sim.violations.length === 0, `seed=${seed} invariant violations:\n${sim.violations.join('\n')}`);
    assert(reclaimed === ids.length, `seed=${seed}: all objects should eventually be reclaimed`);
  }
});

// --- runner ---

rmSync(DATA_ROOT, { recursive: true, force: true });

let failed = 0;
for (const s of scenarios) {
  try {
    s.run();
    console.log(`PASS  ${s.name}`);
  } catch (err) {
    failed += 1;
    console.log(`FAIL  ${s.name}\n      ${(err as Error).message}`);
  }
}
console.log(failed === 0 ? `\nAll ${scenarios.length} scenarios passed.` : `\n${failed} scenario(s) failed.`);
process.exit(failed === 0 ? 0 : 1);
