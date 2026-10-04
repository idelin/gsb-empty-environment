import { Checker } from './checker';
import { MigrationCoordinator } from './coordinator';
import { UnreliableLink } from './link';
import { mulberry32 } from './prng';
import { ShardServer } from './shard';
import { ClientRequest, ClientResponse, LinkMessage, Mutation } from './types';

export interface ScenarioConfig {
  name: string;
  seed: number;
  keySpace: number;
  prefill: number;
  clientOps: number;
  postSwitchOps: number;
  deleteProb: number;
  retryProb: number;
  baseChunkSize: number;
  verbose: boolean;
}

export interface ScenarioResult {
  name: string;
  passed: boolean;
  switchoverCompleted: boolean;
  acked: number;
  rejected: number;
  cachedReplies: number;
  duplicatedMsgs: number;
  violations: string[];
}

type Event =
  | { t: number; seq: number; kind: 'client' }
  | { t: number; seq: number; kind: 'deliver'; msg: LinkMessage }
  | { t: number; seq: number; kind: 'coordinator' }
  | { t: number; seq: number; kind: 'response'; req: ClientRequest; res: ClientResponse }
  | { t: number; seq: number; kind: 'retry'; req: ClientRequest; targetName: string }
  | { t: number; seq: number; kind: 'checkpoint' }
  | { t: number; seq: number; kind: 'final' };

type DistributiveOmit<T, K extends keyof any> = T extends any ? Omit<T, K> : never;
type EventInput = DistributiveOmit<Event, 'seq'>;

class EventQueue {
  private items: Event[] = [];
  private counter = 0;

  push(e: EventInput): void {
    const ev = { ...e, seq: this.counter++ } as Event;
    this.items.push(ev);
    let i = this.items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      const a = this.items[i];
      const b = this.items[p];
      if (a.t < b.t || (a.t === b.t && a.seq < b.seq)) {
        this.items[i] = b;
        this.items[p] = a;
        i = p;
      } else break;
    }
  }

  pop(): Event | undefined {
    const top = this.items[0];
    const last = this.items.pop();
    if (this.items.length > 0 && last) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        for (const c of [l, r]) {
          if (c < this.items.length) {
            const x = this.items[c];
            const y = this.items[m];
            if (x.t < y.t || (x.t === y.t && x.seq < y.seq)) m = c;
          }
        }
        if (m === i) break;
        const tmp = this.items[i];
        this.items[i] = this.items[m];
        this.items[m] = tmp;
        i = m;
      }
    }
    return top;
  }

  get size(): number {
    return this.items.length;
  }
}

export function runScenario(cfg: ScenarioConfig): ScenarioResult {
  const rand = mulberry32(cfg.seed);
  const lines: string[] = [];
  const trace = (msg: string): void => {
    if (cfg.verbose) lines.push(msg);
  };

  const a = new ShardServer('A', trace);
  const b = new ShardServer('B', trace);
  a.activate(0);

  const queue = new EventQueue();
  const link = new UnreliableLink(rand, (at, msg) => queue.push({ t: at, kind: 'deliver', msg }));
  const coord = new MigrationCoordinator(a, b, link, trace, cfg.baseChunkSize);
  const checker = new Checker();

  let reqCounter = 0;
  let rejected = 0;
  let cachedReplies = 0;
  let inFlight = 0;
  let postSwitchBudget = cfg.postSwitchOps;
  let doneHandled = false;
  const ackedIds = new Set<string>();
  const resubmitQueue: ClientRequest[] = [];

  const keyOf = (): string => `k${Math.floor(rand() * cfg.keySpace)}`;

  const issueOp = (now: number, forced?: ClientRequest): void => {
    const target = coord.phase === 'done' ? b : a;
    const req: ClientRequest =
      forced ??
      ((): ClientRequest => {
        const op = rand() < cfg.deleteProb ? 'delete' : 'put';
        return {
          requestId: `r${reqCounter++}`,
          op,
          key: keyOf(),
          value: op === 'put' ? `v${Math.floor(rand() * 1000)}` : undefined,
        };
      })();
    const res = target.handleRequest(req, now);
    if (res.status === 'ok') {
      const m: Mutation = {
        key: req.key,
        value: req.op === 'put' ? req.value! : null,
        version: res.version!,
        requestId: req.requestId,
      };
      checker.onAccepted(m);
      inFlight++;
      const delay = 1 + Math.floor(rand() * 5);
      queue.push({ t: now + delay, kind: 'response', req, res });
      if (rand() < cfg.retryProb) {
        queue.push({ t: now + 6 + Math.floor(rand() * 9), kind: 'retry', req, targetName: target.name });
      }
    } else if (res.status === 'fenced') {
      rejected++;
      trace(`[t=${now}] CLIENT: ${req.requestId} rejected by A (fenced); will resubmit to B after switchover`);
      resubmitQueue.push({ ...req, requestId: `r${reqCounter++}` });
    }
  };

  // Prefill A with base data before migration starts.
  for (let i = 0; i < cfg.prefill; i++) {
    const req: ClientRequest = {
      requestId: `seed${i}`,
      op: 'put',
      key: `k${i % cfg.keySpace}`,
      value: `seed-${i}`,
    };
    const res = a.handleRequest(req, 0);
    const m: Mutation = { key: req.key, value: req.value!, version: res.version!, requestId: req.requestId };
    checker.onAccepted(m);
    checker.onAck(m);
  }

  // Schedule the workload; the coordinator and checkpoints reschedule
  // themselves until the switchover completes.
  let t = 1;
  for (let i = 0; i < cfg.clientOps; i++) {
    queue.push({ t, kind: 'client' });
    t += 1 + Math.floor(rand() * 2);
  }
  queue.push({ t: 1, kind: 'coordinator' });
  queue.push({ t: 10, kind: 'checkpoint' });

  let ev: Event | undefined;
  let guard = 0;
  while ((ev = queue.pop()) !== undefined && guard++ < 200000) {
    const now = ev.t;
    switch (ev.kind) {
      case 'client':
        if (inFlight >= 3) {
          queue.push({ t: now + 1, kind: 'client' });
        } else if (coord.phase === 'done') {
          if (postSwitchBudget > 0) {
            postSwitchBudget--;
            issueOp(now);
          }
        } else {
          issueOp(now);
        }
        break;
      case 'deliver':
        b.applyReplication(ev.msg.mutation, now, ev.msg.channel);
        break;
      case 'coordinator':
        coord.tick(now);
        if (coord.phase !== 'done') queue.push({ t: now + 3, kind: 'coordinator' });
        break;
      case 'response':
        inFlight--;
        if (ev.res.status === 'ok' && !ackedIds.has(ev.req.requestId)) {
          ackedIds.add(ev.req.requestId);
          checker.onAck({
            key: ev.req.key,
            value: ev.req.op === 'put' ? ev.req.value! : null,
            version: ev.res.version!,
            requestId: ev.req.requestId,
          });
          trace(`[t=${now}] CLIENT: ${ev.req.requestId} ACKED (v${ev.res.version})`);
        }
        break;
      case 'retry': {
        const shard = ev.targetName === 'A' ? a : b;
        const res = shard.handleRequest(ev.req, now);
        if (res.status === 'ok') cachedReplies++;
        break;
      }
      case 'checkpoint': {
        const v = checker.visibility(b);
        trace(
          `[t=${now}] CHECK: ${v.visible}/${v.total} acked keys visible on B ` +
            `(B highWater=v${b.highWater}, phase=${coord.phase})`,
        );
        queue.push({ t: now + 25, kind: 'checkpoint' });
        break;
      }
      case 'final':
        break;
    }

    // Step-by-step invariant check after every single event.
    checker.checkStep(now, b);

    if (coord.phase === 'done' && !doneHandled) {
      doneHandled = true;
      for (let i = 0; i < cfg.postSwitchOps; i++) {
        queue.push({ t: now + 1 + i, kind: 'client' });
      }
      queue.push({ t: now + 60, kind: 'final' });
      trace(`[t=${now}] CLIENT: switching over to B; ${resubmitQueue.length} rejected writes resubmitted`);
      // Re-issue the resubmitted requests against B.
      for (const req of resubmitQueue) issueOp(now + 1, req);
    }
    if (ev.kind === 'final') break;
  }

  const passed = checker.finalCheck(b) && doneHandled;
  const v = checker.visibility(b);
  lines.push(`--- ${cfg.name} ---`);
  if (!doneHandled) lines.push('ERROR: switchover never completed');
  lines.push(
    `acked writes: ${checker.totalAcked}, visible on B: ${v.visible}/${v.total} keys, ` +
      `rejected-at-fence: ${rejected}, cached old-request replies: ${cachedReplies}, ` +
      `duplicate msgs on link: ${link.duplicated}/${link.sent}`,
  );
  if (passed) {
    lines.push('RESULT: PASS - every acknowledged write is visible on B; no deleted record resurrected');
  } else {
    lines.push(`RESULT: FAIL - ${checker.violations.length} violation(s)`);
    for (const viol of checker.violations.slice(0, 10)) lines.push(`  ! ${viol}`);
  }
  console.log(lines.join('\n'));

  return {
    name: cfg.name,
    passed,
    switchoverCompleted: doneHandled,
    acked: checker.totalAcked,
    rejected,
    cachedReplies,
    duplicatedMsgs: link.duplicated,
    violations: checker.violations,
  };
}
