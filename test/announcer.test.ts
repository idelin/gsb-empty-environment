import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { StatusAnnouncer } from '../src/announcer.ts';
import type { DropReason, StatusMessage } from '../src/types.ts';

const SPEAK_MS = 100;
const RETRY_MS = 50;

interface Harness {
  clock: ManualClock;
  announcer: StatusAnnouncer;
  events: string[];
  acks: string[];
  completed: string[];
  spoken: string[];
  drops: Array<[string, DropReason]>;
  setSpeakBehavior(behavior: (m: StatusMessage) => boolean | void): void;
}

function makeHarness(): Harness {
  const clock = new ManualClock();
  const events: string[] = [];
  const acks: string[] = [];
  const completed: string[] = [];
  const spoken: string[] = [];
  const drops: Array<[string, DropReason]> = [];
  let behavior: (m: StatusMessage) => boolean | void = () => {};

  const announcer = new StatusAnnouncer({
    clock,
    urgentStreakLimit: 2,
    retryDelayMs: RETRY_MS,
    estimateDurationMs: () => SPEAK_MS,
    hooks: {
      speak: (m) => {
        spoken.push(m.id);
        events.push(`speak:${m.id}`);
        return behavior(m);
      },
      onStart: (m) => events.push(`start:${m.id}`),
      onComplete: (m) => {
        completed.push(m.id);
        events.push(`complete:${m.id}`);
      },
      onAck: (ackId) => {
        acks.push(ackId);
        events.push(`ack:${ackId}`);
      },
      onDrop: (m, reason) => {
        drops.push([m.id, reason]);
        events.push(`drop:${m.id}:${reason}`);
      },
      onPause: (m) => events.push(`pause:${m.id}`),
      onResume: (m) => events.push(`resume:${m.id}`),
    },
  });

  return {
    clock,
    announcer,
    events,
    acks,
    completed,
    spoken,
    drops,
    setSpeakBehavior: (b) => {
      behavior = b;
    },
  };
}

let seq = 0;
function msg(partial: Partial<StatusMessage> & { entityId: string }): StatusMessage {
  seq += 1;
  return {
    id: `m${seq}`,
    version: 0,
    ackId: `ack-${seq}`,
    priority: 'polite',
    text: `message ${seq}`,
    ttlMs: 1_000,
    ...partial,
  };
}

test('polite messages are delivered FIFO with completion acks', () => {
  const h = makeHarness();
  const a = msg({ entityId: 'e1' });
  const b = msg({ entityId: 'e1' });
  h.announcer.enqueue(a);
  h.announcer.enqueue(b);

  h.clock.advance(SPEAK_MS);
  assert.deepEqual(h.completed, [a.id]);
  assert.deepEqual(h.acks, [a.ackId]);

  h.clock.advance(SPEAK_MS);
  assert.deepEqual(h.completed, [a.id, b.id]);
  assert.deepEqual(h.acks, [a.ackId, b.ackId]);
  assert.ok(h.announcer.isAcknowledged(a.ackId));
});

test('urgent interrupts polite; paused polite resumes afterwards', () => {
  const h = makeHarness();
  const p = msg({ entityId: 'e1' });
  h.announcer.enqueue(p);
  h.clock.advance(40); // p is mid-announcement, 60ms remaining

  const u = msg({ entityId: 'e2', priority: 'urgent' });
  h.announcer.enqueue(u);
  assert.deepEqual(h.events.filter((e) => e.startsWith('pause:')), [`pause:${p.id}`]);

  h.clock.advance(SPEAK_MS); // urgent finishes
  assert.deepEqual(h.completed, [u.id]);
  assert.ok(h.events.includes(`resume:${p.id}`));

  h.clock.advance(60); // remaining of p
  assert.deepEqual(h.completed, [u.id, p.id]);
  assert.deepEqual(h.acks, [u.ackId, p.ackId]);
});

test('paused polite message that expires during urgent speech is not resumed', () => {
  const h = makeHarness();
  const p = msg({ entityId: 'e1', ttlMs: 120 });
  h.announcer.enqueue(p);
  h.clock.advance(40);

  const u = msg({ entityId: 'e2', priority: 'urgent' });
  h.announcer.enqueue(u); // p paused at t=40, expires at t=120
  h.clock.advance(SPEAK_MS); // urgent completes at t=140; p is stale by then

  assert.ok(!h.events.includes(`resume:${p.id}`), 'must not resume stale message');
  assert.ok(!h.completed.includes(p.id), 'must not complete stale message');
  assert.deepEqual(h.drops, [[p.id, 'expired']]);
  assert.deepEqual(h.completed, [u.id]);
});

test('paused polite message is dropped when its entity is retracted mid-urgent', () => {
  const h = makeHarness();
  const p = msg({ entityId: 'e1' });
  h.announcer.enqueue(p);
  h.clock.advance(40);

  const u = msg({ entityId: 'e2', priority: 'urgent' });
  h.announcer.enqueue(u);
  h.announcer.retractEntity('e1');

  h.clock.runUntilIdle();
  assert.deepEqual(h.drops, [[p.id, 'entity-retracted']]);
  assert.deepEqual(h.completed, [u.id]);
  assert.ok(!h.events.includes(`resume:${p.id}`));
});

test('version correction invalidates queued messages and rejects stale enqueues', () => {
  const h = makeHarness();
  const speaker = msg({ entityId: 'busy' });
  h.announcer.enqueue(speaker); // occupies the announcer

  const stale = msg({ entityId: 'e1', version: 1 });
  h.announcer.enqueue(stale);
  h.announcer.correctEntityVersion('e1', 2); // correction arrives before delivery
  assert.deepEqual(h.drops, [[stale.id, 'stale-version']]);

  const rejected = msg({ entityId: 'e1', version: 1 });
  assert.equal(h.announcer.enqueue(rejected), false);
  assert.deepEqual(h.drops, [
    [stale.id, 'stale-version'],
    [rejected.id, 'stale-version'],
  ]);

  const fresh = msg({ entityId: 'e1', version: 2 });
  assert.equal(h.announcer.enqueue(fresh), true);

  h.clock.runUntilIdle();
  assert.deepEqual(h.completed, [speaker.id, fresh.id]);
  assert.ok(!h.spoken.includes(stale.id));
});

test('same mergeKey coalesces, keeping the newest message', () => {
  const h = makeHarness();
  const speaker = msg({ entityId: 'busy' });
  h.announcer.enqueue(speaker);

  const first = msg({ entityId: 'e1', mergeKey: 'progress', text: '10%' });
  const second = msg({ entityId: 'e1', mergeKey: 'progress', text: '80%' });
  h.announcer.enqueue(first);
  h.announcer.enqueue(second);

  assert.deepEqual(h.drops, [[first.id, 'merged']]);
  h.clock.runUntilIdle();
  assert.ok(!h.spoken.includes(first.id), 'merged-away message never announced');
  assert.deepEqual(h.completed, [speaker.id, second.id]);
});

test('queued message expiring before its turn is never announced', () => {
  const h = makeHarness();
  const speaker = msg({ entityId: 'busy' });
  h.announcer.enqueue(speaker);

  const short = msg({ entityId: 'e1', ttlMs: 80 });
  h.announcer.enqueue(short);
  h.clock.advance(SPEAK_MS); // speaker completes at t=100; short expired at t=80

  assert.deepEqual(h.drops, [[short.id, 'expired']]);
  assert.ok(!h.spoken.includes(short.id));
  h.clock.runUntilIdle();
  assert.deepEqual(h.completed, [speaker.id]);
});

test('cancelling a status drops queued and in-flight messages', () => {
  const h = makeHarness();
  const a = msg({ entityId: 'e1' });
  const b = msg({ entityId: 'e1' });
  const other = msg({ entityId: 'e2' });
  h.announcer.enqueue(a); // speaking
  h.announcer.enqueue(b); // queued
  h.announcer.enqueue(other); // queued

  h.announcer.cancelEntity('e1');
  assert.deepEqual(h.drops, [
    [b.id, 'cancelled'],
    [a.id, 'cancelled'],
  ]);

  h.clock.runUntilIdle();
  assert.ok(!h.completed.includes(a.id));
  assert.ok(!h.completed.includes(b.id));
  assert.deepEqual(h.completed, [other.id]);
});

test('delivery retries never produce a duplicate acknowledgement', () => {
  const h = makeHarness();
  let attempts = 0;
  h.setSpeakBehavior(() => {
    attempts += 1;
    return attempts >= 3; // fail twice, succeed on the third attempt
  });

  const a = msg({ entityId: 'e1' });
  h.announcer.enqueue(a); // attempt 1 fails at t=0
  h.clock.advance(RETRY_MS); // attempt 2 fails at t=50
  h.clock.advance(RETRY_MS); // attempt 3 succeeds at t=100
  h.clock.advance(SPEAK_MS); // completes at t=200

  assert.equal(attempts, 3);
  assert.deepEqual(h.acks, [a.ackId]);
  assert.deepEqual(h.completed, [a.id]);
});

test('message expiring during delivery retries is dropped, not completed', () => {
  const h = makeHarness();
  h.setSpeakBehavior(() => false); // always fail
  const a = msg({ entityId: 'e1', ttlMs: 120 });
  h.announcer.enqueue(a);
  h.clock.runUntilIdle();
  assert.deepEqual(h.drops, [[a.id, 'expired']]);
  assert.deepEqual(h.acks, []);
  assert.deepEqual(h.completed, []);
});

test('entity retraction invalidates queued and in-flight messages', () => {
  const h = makeHarness();
  const a = msg({ entityId: 'e1' });
  const b = msg({ entityId: 'e1' });
  h.announcer.enqueue(a);
  h.announcer.enqueue(b);

  h.announcer.retractEntity('e1');
  assert.deepEqual(h.drops, [
    [b.id, 'entity-retracted'],
    [a.id, 'entity-retracted'],
  ]);

  // New messages for a retracted entity are rejected at intake.
  const late = msg({ entityId: 'e1' });
  assert.equal(h.announcer.enqueue(late), false);

  h.clock.runUntilIdle();
  assert.deepEqual(h.completed, []);
  assert.deepEqual(h.acks, []);
});

test('urgent streak is bounded: polite messages cannot starve', () => {
  const h = makeHarness();
  const first = msg({ entityId: 'u', priority: 'urgent' });
  h.announcer.enqueue(first); // speaking

  const polite = msg({ entityId: 'p' });
  h.announcer.enqueue(polite); // queued behind urgent traffic

  // Relentless urgent producer: after each advance another urgent arrives.
  let urgentCount = 1;
  const maxUrgents = 9;

  // Drive manually: after each urgent completes, enqueue a new urgent.
  for (let i = 0; i < maxUrgents + 3; i++) {
    h.clock.advance(SPEAK_MS);
    if (h.completed.includes(polite.id)) break;
    if (urgentCount < maxUrgents) {
      urgentCount += 1;
      h.announcer.enqueue(msg({ entityId: 'u', priority: 'urgent' }));
    }
  }

  assert.ok(h.completed.includes(polite.id), 'polite message eventually delivered');
  const politeIndex = h.completed.indexOf(polite.id);
  assert.ok(
    politeIndex <= 2,
    `polite delivered after at most 2 consecutive urgents, got index ${politeIndex}`,
  );
});

test('unmounting a component cleans up its pending and in-flight messages', () => {
  const h = makeHarness();
  const a = msg({ entityId: 'e1', ownerId: 'panel-1' });
  const b = msg({ entityId: 'e2', ownerId: 'panel-2' });
  const c = msg({ entityId: 'e3', ownerId: 'panel-2' });
  h.announcer.enqueue(a); // speaking
  h.announcer.enqueue(b); // queued
  h.announcer.enqueue(c); // queued

  h.announcer.unmount('panel-2');
  assert.deepEqual(h.drops, [
    [b.id, 'owner-unmounted'],
    [c.id, 'owner-unmounted'],
  ]);

  h.announcer.unmount('panel-1'); // a is mid-announcement
  assert.deepEqual(h.drops, [
    [b.id, 'owner-unmounted'],
    [c.id, 'owner-unmounted'],
    [a.id, 'owner-unmounted'],
  ]);

  h.clock.runUntilIdle();
  assert.deepEqual(h.completed, []);
  assert.deepEqual(h.acks, []);
});

test('randomized workload: no stale announcements, everything live terminates', () => {
  // Deterministic PRNG (mulberry32).
  let seed = 0x2f6e2b1;
  const rand = () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const pick = <T>(items: T[]): T => items[Math.floor(rand() * items.length)];

  const clock = new ManualClock();
  const acks: string[] = [];
  const terminal = new Map<string, 'completed' | DropReason>();
  const accepted = new Set<string>();

  const announcer = new StatusAnnouncer({
    clock,
    urgentStreakLimit: 2,
    retryDelayMs: 40,
    estimateDurationMs: () => 60,
    hooks: {
      speak: (m) => {
        // Core invariant: nothing stale is ever announced.
        assert.ok(
          announcer.isLive(m),
          `stale announcement of ${m.id} at t=${clock.now()}`,
        );
      },
      onAck: (ackId) => {
        assert.ok(!acks.includes(ackId), `duplicate ack ${ackId}`);
        acks.push(ackId);
      },
      onComplete: (m) => {
        assert.ok(!terminal.has(m.id), `double completion of ${m.id}`);
        terminal.set(m.id, 'completed');
      },
      onDrop: (m, reason) => {
        if (accepted.has(m.id)) {
          assert.ok(!terminal.has(m.id), `double terminal state for ${m.id}`);
          terminal.set(m.id, reason);
        }
      },
    },
  });

  const entities = ['e1', 'e2', 'e3', 'e4'];
  const owners = ['o1', 'o2', 'o3'];
  const versions = new Map<string, number>(entities.map((e) => [e, 0]));

  for (let round = 0; round < 400; round++) {
    const roll = rand();
    if (roll < 0.55) {
      const entityId = pick(entities);
      const message = msg({
        entityId,
        version: versions.get(entityId)!,
        priority: rand() < 0.3 ? 'urgent' : 'polite',
        ttlMs: 50 + Math.floor(rand() * 550),
        mergeKey: rand() < 0.3 ? `mk-${Math.floor(rand() * 3)}` : undefined,
        ownerId: rand() < 0.4 ? pick(owners) : undefined,
      });
      if (announcer.enqueue(message)) accepted.add(message.id);
    } else if (roll < 0.65) {
      const entityId = pick(entities);
      const bump = versions.get(entityId)! + 1 + Math.floor(rand() * 2);
      versions.set(entityId, bump);
      announcer.correctEntityVersion(entityId, bump);
    } else if (roll < 0.72) {
      announcer.retractEntity(pick(entities));
    } else if (roll < 0.8) {
      announcer.cancelEntity(pick(entities));
    } else if (roll < 0.86) {
      announcer.unmount(pick(owners));
    } else {
      clock.advance(Math.floor(rand() * 120));
    }
  }

  clock.runUntilIdle();

  const state = announcer.inspect();
  assert.deepEqual(state, { queued: 0, paused: 0, speaking: null });

  // Liveness: every accepted message reached exactly one terminal state.
  for (const id of accepted) {
    assert.ok(terminal.has(id), `message ${id} never reached a terminal state`);
  }
  assert.equal(terminal.size, accepted.size);
});
