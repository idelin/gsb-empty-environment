import { describe, expect, test } from 'bun:test';
import { Migrator, MigState } from '../src/migrator';
import type { Scheduler } from '../src/migrator';
import { ColdStore, LocalStore, copyValid } from '../src/store';
import { Journal } from '../src/journal';
import { mulberry32 } from '../src/prng';

class ManualScheduler implements Scheduler {
  now = 0;
  private seq = 0;
  private q: Array<{ t: number; seq: number; fn: () => void }> = [];

  after(delay: number, fn: () => void): void {
    this.q.push({ t: this.now + delay, seq: this.seq++, fn });
  }

  run(maxTicks = 1000): void {
    let ticks = 0;
    while (this.q.length > 0 && ticks < maxTicks) {
      this.q.sort((a, b) => a.t - b.t || a.seq - b.seq);
      const ev = this.q.shift()!;
      this.now = ev.t;
      ev.fn();
      ticks++;
    }
  }
}

const CFG = { uploadLatency: 5, receiptTimeout: 8, verifyDelay: 1, reclaimPoll: 3 };

function setup(opts: { dropReceipt?: boolean } = {}) {
  const sched = new ManualScheduler();
  const local = new LocalStore();
  const cold = new ColdStore();
  const journal = new Journal();
  const logs: string[] = [];
  const build = (id: string, extra: Record<string, unknown> = {}) =>
    new Migrator(id, {
      local,
      cold,
      journal,
      scheduler: sched,
      config: CFG,
      log: (m) => logs.push(m),
      onUploadAck: (receipt, m) => {
        if (!opts.dropReceipt) sched.after(2, () => m.deliverReceipt(receipt));
      },
      ...extra,
    });
  return { sched, local, cold, journal, logs, build };
}

describe('migration state machine', () => {
  test('clean run: reclaim only after read-back match and persisted decision', () => {
    const { sched, local, cold, journal, build } = setup();
    local.write('o', 'hello-v1');
    const m = build('o');
    m.start();
    sched.run();
    expect(m.state).toBe(MigState.Reclaimed);
    expect(local.read('o')).toBeUndefined();
    const remote = cold.read('o')!;
    expect(copyValid(remote)).toBe(true);
    expect(remote.version).toBe(1);
    expect(journal.find('o', 1, remote.digest)).toBeDefined();
  });

  test('receipt loss: falls back to read-back after timeout', () => {
    const { sched, local, logs, build } = setup({ dropReceipt: true });
    local.write('o', 'hello-v1');
    const m = build('o');
    m.start();
    sched.run();
    expect(m.state).toBe(MigState.Reclaimed);
    expect(local.read('o')).toBeUndefined();
    expect(logs.some((l) => l.includes('receipt lost'))).toBe(true);
  });

  test('old receipt cannot confirm a newer version', () => {
    const { sched, local, cold, journal, build } = setup();
    local.write('o', 'v1');
    const m = build('o');
    m.start();
    sched.after(6, () => local.write('o', 'v2')); // update lands before receipt arrives
    sched.run();
    expect(m.state).toBe(MigState.Aborted);
    expect(m.abortReason).toBe('stale-receipt');
    expect(local.read('o')!.version).toBe(2); // new version untouched
    expect(cold.read('o')!.version).toBe(1); // remote still holds old upload
    expect(journal.size()).toBe(0); // no decision was persisted
  });

  test('open read handle delays reclaim until it closes', () => {
    const { sched, local, build } = setup();
    local.write('o', 'hot');
    local.openHandle('o');
    const m = build('o');
    m.start();
    sched.run(40); // bounded: reclaim keeps polling while the handle is open
    expect(m.state).toBe(MigState.Releasing);
    expect(local.read('o')).toBeDefined(); // not deleted while handle open
    local.closeHandle('o');
    sched.run();
    expect(m.state).toBe(MigState.Reclaimed);
    expect(local.read('o')).toBeUndefined();
  });

  test('unpersisted decision blocks reclaim', () => {
    const { sched, local, journal, build } = setup();
    local.write('o', 'data');
    journal.failNextFlush = true;
    const m = build('o');
    m.start();
    sched.run();
    expect(m.state).toBe(MigState.Aborted);
    expect(m.abortReason).toBe('decision-not-persisted');
    expect(local.read('o')).toBeDefined();
  });

  test('target corrupted before verify: abort, local retained', () => {
    const rng = mulberry32(1);
    const { sched, local, cold, build } = setup();
    local.write('o', 'data');
    const m = build('o');
    m.start();
    sched.after(7, () => cold.corrupt('o', rng)); // after upload (t=5), before verify (t=8)
    sched.run();
    expect(m.state).toBe(MigState.Aborted);
    expect(m.abortReason).toBe('target-corrupted');
    expect(local.read('o')).toBeDefined();
  });

  test('target corrupted after decision: final revalidation blocks reclaim', () => {
    const rng = mulberry32(2);
    const { sched, local, cold, build } = setup();
    local.write('o', 'data');
    const m = build('o', {
      onDecision: (_d: unknown, mm: Migrator) => {
        sched.after(1, () => cold.corrupt(mm.objectId, rng));
      },
    });
    m.start();
    sched.run();
    expect(m.state).toBe(MigState.Aborted);
    expect(m.abortReason).toBe('target-unhealthy-before-reclaim');
    expect(local.read('o')).toBeDefined();
  });

  test('update after decision: old decision cannot reclaim new version', () => {
    const { sched, local, build } = setup();
    local.write('o', 'v1');
    const m = build('o', {
      onDecision: () => sched.after(1, () => local.write('o', 'v2')),
    });
    m.start();
    sched.run();
    expect(m.state).toBe(MigState.Aborted);
    expect(m.abortReason).toBe('superseded-by-update');
    expect(local.read('o')!.version).toBe(2);
  });

  test('local store refuses deletion while handles are open', () => {
    const local = new LocalStore();
    local.write('o', 'x');
    local.openHandle('o');
    expect(() => local.remove('o')).toThrow();
    local.closeHandle('o');
    expect(local.remove('o')).toBeDefined();
  });
});
