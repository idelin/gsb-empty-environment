import { MigState, Migrator } from './migrator';
import type { Decision } from './journal';
import { World, defaultTiming, noFaults } from './world';
import type { WorldConfig } from './world';
import { randInt } from './prng';

interface ScenarioResult {
  name: string;
  ok: boolean;
  error?: string;
  detail: string;
}

function expect(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`expectation failed: ${msg}`);
}

function makeWorld(cfg: Partial<WorldConfig> & Pick<WorldConfig, 'seed' | 'faults'>): World {
  return new World({ ...defaultTiming, ...cfg });
}

function scenario(
  name: string,
  cfg: Partial<WorldConfig> & Pick<WorldConfig, 'seed' | 'faults'>,
  body: (w: World) => void,
  check: (w: World) => void,
): ScenarioResult {
  const world = makeWorld(cfg);
  try {
    body(world);
    world.run();
    check(world);
    return { name, ok: true, detail: summarize(world) };
  } catch (err) {
    return { name, ok: false, error: (err as Error).message, detail: summarize(world) };
  }
}

function summarize(w: World): string {
  const parts: string[] = [];
  for (const [id, tr] of w.objects) {
    const state = tr.migrator?.state ?? 'n/a';
    const aborts = tr.abortReasons.length ? ` aborts=[${tr.abortReasons.join(',')}]` : '';
    parts.push(`${id}:${state}${aborts}`);
  }
  return parts.join(' | ');
}

const results: ScenarioResult[] = [];

// 1. Clean run: upload -> receipt -> read-back -> persist decision -> reclaim.
results.push(
  scenario(
    'clean: 正常迁移并回收本地副本',
    { seed: 1, faults: noFaults },
    (w) => {
      for (const id of ['a', 'b', 'c']) {
        w.writeObject(id, `payload-${id}`);
        w.migrate(id);
      }
    },
    (w) => {
      for (const id of ['a', 'b', 'c']) {
        expect(w.objects.get(id)!.migrator!.state === MigState.Reclaimed, `${id} should be RECLAIMED`);
        expect(w.local.read(id) === undefined, `${id} local copy should be gone`);
      }
    },
  ),
);

// 2. Receipt loss: safety must come from read-back, not from the receipt.
results.push(
  scenario(
    'receipt-loss: 回执全部丢失仍可通过回读完成',
    { seed: 2, faults: { ...noFaults, loseReceiptProb: 1 } },
    (w) => {
      for (const id of ['a', 'b']) {
        w.writeObject(id, `payload-${id}`);
        w.migrate(id);
      }
    },
    (w) => {
      for (const id of ['a', 'b']) {
        expect(w.objects.get(id)!.migrator!.state === MigState.Reclaimed, `${id} should be RECLAIMED`);
      }
      expect(
        w.logs.some((l) => l.includes('receipt lost')),
        'should have taken the receipt-timeout read-back path',
      );
    },
  ),
);

// 3. Update race + replayed old receipt: the old receipt must not confirm the
//    new version; the round aborts and a later round migrates the new version.
results.push(
  scenario(
    'update-race: 更新竞争 + 旧回执重放不能确认新版对象',
    { seed: 3, faults: { ...noFaults, replayOldReceipts: true } },
    (w) => {
      w.writeObject('o', 'v1-bytes'); // v1
      w.migrate('o');
      w.after(6, () => w.writeObject('o', 'v2-bytes')); // v2 lands mid-migration
    },
    (w) => {
      const tr = w.objects.get('o')!;
      expect(tr.migrator!.state === MigState.Reclaimed, 'o should eventually be RECLAIMED');
      expect(
        tr.abortReasons.includes('stale-receipt'),
        `expected a stale-receipt abort, got [${tr.abortReasons.join(',')}]`,
      );
      expect(tr.latestVersion === 2, 'latest version should be v2');
      expect(w.local.read('o') === undefined, 'local copy of v2 reclaimed');
      const remote = w.cold.read('o');
      expect(remote !== undefined && remote.version === 2, 'remote holds v2');
    },
  ),
);

// 4a. Target corrupted between upload and verify: abort, local retained, retry succeeds.
results.push(
  scenario(
    'target-corruption: 上传后目标损坏，校验失败且本地保留',
    { seed: 4, faults: noFaults },
    (w) => {
      w.writeObject('o', 'precious-bytes');
      w.migrate('o');
      w.after(6, () => {
        w.cold.corrupt('o', w.rng);
        w.log('[t=6] o FAULT: target corrupted before verify');
      });
    },
    (w) => {
      const tr = w.objects.get('o')!;
      expect(tr.abortReasons.includes('target-corrupted'), 'should abort with target-corrupted');
      expect(tr.migrator!.state === MigState.Reclaimed, 'retry should reclaim');
    },
  ),
);

// 4b. Target corrupted after the decision was persisted: the final
//     revalidation before reclaim must catch it and keep the local copy.
results.push(
  scenario(
    'post-decision-corruption: 决议后目标损坏，回收前最终校验拦截',
    { seed: 5, faults: noFaults },
    (w) => {
      w.writeObject('o', 'precious-bytes');
      const hook = (_d: Decision, m: Migrator) => {
        w.onDecisionHooks.splice(w.onDecisionHooks.indexOf(hook), 1); // one-shot
        w.after(1, () => {
          w.cold.corrupt(m.objectId, w.rng);
          w.log(`[t=${w.now}] ${m.objectId} FAULT: target corrupted after decision`);
        });
      };
      w.onDecisionHooks.push(hook);
      w.migrate('o');
    },
    (w) => {
      const tr = w.objects.get('o')!;
      expect(
        tr.abortReasons.includes('target-unhealthy-before-reclaim'),
        `expected target-unhealthy-before-reclaim, got [${tr.abortReasons.join(',')}]`,
      );
      expect(tr.migrator!.state === MigState.Reclaimed, 'retry should reclaim');
    },
  ),
);

// 5. A reader holding a local handle delays reclaim until the handle closes.
results.push(
  scenario(
    'handle-delay: 读取句柄延迟本地回收',
    { seed: 6, faults: noFaults },
    (w) => {
      w.writeObject('o', 'hot-data');
      w.openReader('o', 30); // closes at t=30
      w.migrate('o');
    },
    (w) => {
      const tr = w.objects.get('o')!;
      expect(tr.migrator!.state === MigState.Reclaimed, 'o should be RECLAIMED after handle closes');
      expect(tr.reclaimTime !== null && tr.reclaimTime >= 30, `reclaim at t=${tr.reclaimTime} must wait for t>=30`);
      expect(w.logs.some((l) => l.includes('reclaim delayed')), 'should log delayed reclaim');
    },
  ),
);

// 6. Decision flush fails: no persisted decision -> no reclaim.
results.push(
  scenario(
    'journal-failure: 决议未持久化则不回收',
    { seed: 7, faults: noFaults },
    (w) => {
      w.writeObject('o', 'precious-bytes');
      w.journal.failNextFlush = true;
      w.migrate('o');
    },
    (w) => {
      const tr = w.objects.get('o')!;
      expect(
        tr.abortReasons.includes('decision-not-persisted'),
        `expected decision-not-persisted, got [${tr.abortReasons.join(',')}]`,
      );
      expect(tr.migrator!.state === MigState.Reclaimed, 'retry should reclaim');
    },
  ),
);

// 7. Chaos: all faults + random writers/readers over many objects. The safety
//    invariant (>= 1 valid copy of the latest version) is checked after every
//    event; any violation throws and fails the scenario.
results.push(
  scenario(
    'chaos: 全故障注入下任意时刻至少一份有效副本',
    {
      seed: 42,
      faults: {
        loseReceiptProb: 0.3,
        replayOldReceipts: true,
        corruptAfterUploadProb: 0.2,
        corruptAfterDecisionProb: 0.15,
        journalFlushFailProb: 0.15,
      },
      maxRounds: 10,
    },
    (w) => {
      for (let i = 0; i < 40; i++) {
        const id = `obj-${i}`;
        w.writeObject(id, w.randomBytes(32));
        w.after(randInt(w.rng, 0, 20), () => w.migrate(id));
        const updates = randInt(w.rng, 0, 3);
        for (let u = 0; u < updates; u++) {
          w.after(randInt(w.rng, 5, 150), () => w.writeObject(id, w.randomBytes(32)));
        }
        const readers = randInt(w.rng, 0, 2);
        for (let r = 0; r < readers; r++) {
          w.after(randInt(w.rng, 0, 60), () => w.openReader(id, randInt(w.rng, 2, 15)));
        }
      }
    },
    (w) => {
      let reclaimed = 0;
      for (const [, tr] of w.objects) {
        if (tr.migrator?.state === MigState.Reclaimed) reclaimed++;
      }
      expect(reclaimed >= 20, `expected at least 20 reclaimed under chaos, got ${reclaimed}`);
      console.log(`    chaos stats: ${reclaimed}/${w.objects.size} reclaimed, journal size=${w.journal.size()}`);
    },
  ),
);

let failed = 0;
for (const r of results) {
  if (r.ok) {
    console.log(`PASS  ${r.name}`);
    console.log(`      ${r.detail}`);
  } else {
    failed++;
    console.log(`FAIL  ${r.name}`);
    console.log(`      ${r.error}`);
    console.log(`      ${r.detail}`);
  }
}
console.log(failed === 0 ? `\nAll ${results.length} scenarios passed.` : `\n${failed} scenario(s) failed.`);
process.exit(failed === 0 ? 0 : 1);
