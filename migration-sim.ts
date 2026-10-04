type LinkId = "old" | "new";

type Pkt = { kind: "data"; seq: number } | { kind: "ack"; cum: number };

interface Io {
  readonly now: number;
  emit(linkId: LinkId, pkt: Pkt): void;
  after(delay: number, cb: () => void): void;
  linkOpen(linkId: LinkId): boolean;
}

class Rng {
  state: number;
  constructor(seed: number) {
    this.state = seed >>> 0;
  }
  next(): number {
    this.state = (this.state + 0x6d2b79f5) >>> 0;
    let t = this.state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  }
}

class Sender {
  total: number;
  window: number;
  rto: number;
  active: LinkId = "old";
  nextSeq = 0;
  acked = 0;
  sentMax: Record<LinkId, number> = { old: -1, new: -1 };
  linkAck: Record<LinkId, number> = { old: 0, new: 0 };
  unacked = new Map<number, number>();
  rtoAt: number | null = null;
  rtoGen = 0;
  dataSent = 0;
  retransmits = 0;
  clampedAcks = 0;
  maxUnacked = 0;

  constructor(total: number, window: number, rto: number) {
    this.total = total;
    this.window = window;
    this.rto = rto;
  }

  sendAvailable(io: Io): void {
    while (this.nextSeq < this.total && this.nextSeq < this.acked + this.window) {
      this.transmit(this.nextSeq, io);
      this.nextSeq++;
    }
  }

  transmit(seq: number, io: Io): void {
    if (seq > this.sentMax[this.active]) this.sentMax[this.active] = seq;
    const resend = this.unacked.has(seq);
    this.unacked.set(seq, io.now);
    if (this.unacked.size > this.maxUnacked) this.maxUnacked = this.unacked.size;
    this.dataSent++;
    if (resend) this.retransmits++;
    io.emit(this.active, { kind: "data", seq });
    this.armRto(io);
  }

  armRto(io: Io): void {
    if (this.unacked.size === 0) {
      this.rtoAt = null;
      this.rtoGen++;
      return;
    }
    if (this.rtoAt === null) {
      this.rtoAt = io.now + this.rto;
      const gen = ++this.rtoGen;
      io.after(this.rto, () => {
        if (this.rtoGen === gen) this.onRto(io);
      });
    }
  }

  onRto(io: Io): void {
    this.rtoAt = null;
    if (this.unacked.size === 0) return;
    const pending = [...this.unacked.keys()].sort((a, b) => a - b);
    for (const seq of pending) this.transmit(seq, io);
  }

  onAck(linkId: LinkId, cum: number, io: Io): void {
    const limit = this.sentMax[linkId] + 1;
    const clamped = Math.min(cum, limit);
    if (clamped < cum) this.clampedAcks++;
    if (clamped > this.linkAck[linkId]) this.linkAck[linkId] = clamped;
    const merged = Math.max(this.linkAck.old, this.linkAck.new);
    if (merged > this.acked) {
      this.acked = merged;
      for (const seq of [...this.unacked.keys()]) {
        if (seq < merged) this.unacked.delete(seq);
      }
      this.sendAvailable(io);
    }
    if (this.acked > this.nextSeq) {
      throw new Error("invariant violated: acked prefix beyond sent prefix");
    }
    this.armRto(io);
  }
}

class Receiver {
  rwnd: number;
  expect = 0;
  buffered = new Set<number>();
  delivered: number[] = [];
  dupData = 0;
  outOfWindow = 0;
  acksSent = 0;
  periodicAcks = 0;
  maxBuffered = 0;

  constructor(rwnd: number) {
    this.rwnd = rwnd;
  }

  onData(linkId: LinkId, seq: number, io: Io): void {
    if (seq < this.expect) {
      this.dupData++;
    } else if (seq >= this.expect + this.rwnd) {
      this.outOfWindow++;
    } else {
      this.buffered.add(seq);
      if (this.buffered.size > this.maxBuffered) this.maxBuffered = this.buffered.size;
      while (this.buffered.delete(this.expect)) {
        this.delivered.push(this.expect);
        this.expect++;
      }
    }
    this.sendAcks(io);
  }

  sendAcks(io: Io): void {
    const ids: LinkId[] = ["old", "new"];
    for (const id of ids) {
      if (!io.linkOpen(id)) continue;
      io.emit(id, { kind: "ack", cum: this.expect });
      this.acksSent++;
    }
  }
}

class Link {
  id: LinkId;
  dropRate: number;
  minDelay: number;
  maxDelay: number;
  open: boolean;
  sent = 0;
  lost = 0;

  constructor(id: LinkId, dropRate: number, minDelay: number, maxDelay: number, open: boolean) {
    this.id = id;
    this.dropRate = dropRate;
    this.minDelay = minDelay;
    this.maxDelay = maxDelay;
    this.open = open;
  }
}

interface LinkCfg {
  dropRate: number;
  minDelay: number;
  maxDelay: number;
}

interface Scenario {
  seed: number;
  total: number;
  window: number;
  rwnd: number;
  rto: number;
  ackInterval: number;
  migrateAt: number;
  oldCloseAt: number;
  maxTime: number;
  oldLink: LinkCfg;
  newLink: LinkCfg;
}

interface Ev {
  t: number;
  k: number;
  cb: () => void;
}

function evLess(a: Ev, b: Ev): boolean {
  return a.t < b.t || (a.t === b.t && a.k < b.k);
}

class EvQueue {
  h: Ev[] = [];
  k = 0;

  get size(): number {
    return this.h.length;
  }

  push(t: number, cb: () => void): void {
    const e: Ev = { t, k: this.k++, cb };
    const h = this.h;
    h.push(e);
    let i = h.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (!evLess(e, h[p])) break;
      h[i] = h[p];
      i = p;
    }
    h[i] = e;
  }

  pop(): Ev | undefined {
    const h = this.h;
    if (h.length === 0) return undefined;
    const top = h[0];
    const last = h.pop() as Ev;
    if (h.length > 0) {
      h[0] = last;
      let i = 0;
      for (;;) {
        let m = i;
        const l = i * 2 + 1;
        const r = l + 1;
        if (l < h.length && evLess(h[l], h[m])) m = l;
        if (r < h.length && evLess(h[r], h[m])) m = r;
        if (m === i) break;
        const tmp = h[i];
        h[i] = h[m];
        h[m] = tmp;
        i = m;
      }
    }
    return top;
  }
}

class Sim {
  cfg: Scenario;
  rng: Rng;
  q = new EvQueue();
  time = 0;
  links: Record<LinkId, Link>;
  sender: Sender;
  receiver: Receiver;
  io: Io;

  constructor(cfg: Scenario) {
    this.cfg = cfg;
    this.rng = new Rng(cfg.seed);
    this.links = {
      old: new Link("old", cfg.oldLink.dropRate, cfg.oldLink.minDelay, cfg.oldLink.maxDelay, true),
      new: new Link("new", cfg.newLink.dropRate, cfg.newLink.minDelay, cfg.newLink.maxDelay, false),
    };
    this.sender = new Sender(cfg.total, cfg.window, cfg.rto);
    this.receiver = new Receiver(cfg.rwnd);
    const self = this;
    this.io = {
      get now() {
        return self.time;
      },
      emit(linkId: LinkId, pkt: Pkt) {
        self.transmit(linkId, pkt);
      },
      after(delay: number, cb: () => void) {
        self.at(self.time + delay, cb);
      },
      linkOpen(linkId: LinkId) {
        return self.links[linkId].open;
      },
    };
  }

  at(t: number, cb: () => void): void {
    this.q.push(t, cb);
  }

  transmit(linkId: LinkId, pkt: Pkt): void {
    const link = this.links[linkId];
    if (!link.open) return;
    link.sent++;
    if (this.rng.next() < link.dropRate) {
      link.lost++;
      return;
    }
    const d = link.minDelay + this.rng.next() * (link.maxDelay - link.minDelay);
    this.at(this.time + d, () => {
      if (pkt.kind === "data") this.receiver.onData(linkId, pkt.seq, this.io);
      else this.sender.onAck(linkId, pkt.cum, this.io);
    });
  }

  run(): void {
    this.at(0, () => this.sender.sendAvailable(this.io));
    this.at(this.cfg.migrateAt, () => {
      this.links.new.open = true;
      this.sender.active = "new";
    });
    this.at(this.cfg.oldCloseAt, () => {
      this.links.old.open = false;
    });
    const ackTick = (): void => {
      if (this.sender.acked >= this.cfg.total) return;
      this.receiver.periodicAcks++;
      this.receiver.sendAcks(this.io);
      this.at(this.time + this.cfg.ackInterval, ackTick);
    };
    this.at(this.cfg.ackInterval, ackTick);
    const done = (): boolean =>
      this.receiver.expect >= this.cfg.total && this.sender.acked >= this.cfg.total;
    while (this.q.size > 0 && this.time <= this.cfg.maxTime && !done()) {
      const e = this.q.pop() as Ev;
      this.time = e.t;
      e.cb();
    }
    if (!done()) throw new Error("simulation did not converge");
  }
}

function check(cond: boolean, msg: string): void {
  if (!cond) throw new Error("CHECK FAILED: " + msg);
}

function testAckMergeClamp(): void {
  const sent: Array<{ link: LinkId; seq: number }> = [];
  const io: Io = {
    get now() {
      return 0;
    },
    emit(linkId: LinkId, pkt: Pkt) {
      if (pkt.kind === "data") sent.push({ link: linkId, seq: pkt.seq });
    },
    after() {},
    linkOpen() {
      return true;
    },
  };
  const s = new Sender(20, 64, 1000);
  for (let i = 0; i < 10; i++) s.transmit(i, io);
  s.active = "new";
  for (let i = 10; i < 20; i++) s.transmit(i, io);
  s.nextSeq = 20;

  s.onAck("old", 20, io);
  check(s.acked === 10, "late old-link ack clamped to old sent prefix, got " + s.acked);

  s.onAck("new", 15, io);
  check(s.acked === 15, "new-link ack advances merged prefix, got " + s.acked);

  s.onAck("old", 20, io);
  check(s.acked === 15, "old-link ack cannot confirm new-link-only data, got " + s.acked);

  s.onAck("new", 20, io);
  check(s.acked === 20, "new-link ack confirms its own data, got " + s.acked);

  s.onAck("old", 7, io);
  check(s.acked === 20, "stale duplicate ack does not regress, got " + s.acked);

  check(s.clampedAcks === 2, "clamp counter, got " + s.clampedAcks);
  check(sent.filter((p) => p.link === "old").length === 10, "old link carried 10");
  check(sent.filter((p) => p.link === "new").length === 10, "new link carried 10");
}

function verify(sim: Sim, expectClamp: boolean): void {
  const s = sim.sender;
  const r = sim.receiver;
  const total = sim.cfg.total;
  check(r.delivered.length === total, "delivered count " + r.delivered.length);
  for (let i = 0; i < total; i++) {
    check(r.delivered[i] === i, "exactly-once in-order delivery broken at " + i);
  }
  check(s.acked === total, "sender acked prefix " + s.acked);
  check(s.unacked.size === 0, "sender buffer drained");
  check(r.buffered.size === 0, "receiver buffer drained");
  check(s.maxUnacked <= sim.cfg.window, "sender memory bound " + s.maxUnacked);
  check(r.maxBuffered <= sim.cfg.rwnd, "receiver memory bound " + r.maxBuffered);
  check(s.retransmits > 0, "sender retransmission exercised");
  check(r.periodicAcks > 0, "receiver ack retransmission exercised");
  check(r.dupData > 0, "duplicate suppression exercised");
  if (expectClamp) check(s.clampedAcks > 0, "old-link ack clamp exercised");
}

function report(sim: Sim): void {
  const s = sim.sender;
  const r = sim.receiver;
  const lo = sim.links.old;
  const ln = sim.links.new;
  console.log(
    "seed=" + sim.cfg.seed,
    "time=" + sim.time.toFixed(1),
    "delivered=" + r.delivered.length,
    "dataSent=" + s.dataSent,
    "retx=" + s.retransmits,
    "dup=" + r.dupData,
    "clampedAcks=" + s.clampedAcks,
    "maxUnacked=" + s.maxUnacked + "/" + sim.cfg.window,
    "maxBuffered=" + r.maxBuffered + "/" + sim.cfg.rwnd,
    "oldLink=" + lo.sent + "/" + lo.lost,
    "newLink=" + ln.sent + "/" + ln.lost,
  );
}

function main(): void {
  testAckMergeClamp();
  console.log("unit: ack-merge clamp ok");

  const base: Scenario = {
    seed: 0,
    total: 1000,
    window: 16,
    rwnd: 16,
    rto: 30,
    ackInterval: 8,
    migrateAt: 150,
    oldCloseAt: 320,
    maxTime: 200000,
    oldLink: { dropRate: 0.12, minDelay: 2, maxDelay: 7 },
    newLink: { dropRate: 0.08, minDelay: 1, maxDelay: 4 },
  };
  for (const seed of [1, 2, 3, 4, 5]) {
    const sim = new Sim({ ...base, seed });
    sim.run();
    verify(sim, true);
    report(sim);
  }
  const stress: Scenario = {
    ...base,
    window: 8,
    rwnd: 8,
    migrateAt: 200,
    oldCloseAt: 360,
    oldLink: { dropRate: 0.25, minDelay: 2, maxDelay: 10 },
    newLink: { dropRate: 0.2, minDelay: 1, maxDelay: 6 },
  };
  for (const seed of [11, 12, 13]) {
    const sim = new Sim({ ...stress, seed });
    sim.run();
    verify(sim, true);
    report(sim);
  }
  console.log("all checks passed");
}

main();
