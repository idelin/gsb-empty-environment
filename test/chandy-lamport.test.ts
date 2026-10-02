import { test } from "node:test";
import assert from "node:assert/strict";
import { Process } from "../src/process.ts";
import { Simulation } from "../src/simulation.ts";
import { snapshotTotal, inFlightTotal } from "../src/snapshot.ts";
import type { Message, TransferMessage } from "../src/messages.ts";

const CLUSTER = { A: 100, B: 100, C: 100 };
const TOTAL = 300;

function runClusterSnapshot(seed: number) {
  const sim = new Simulation({ balances: { ...CLUSTER }, seed });
  sim.scheduleSnapshotInitiation("A", "snap-1", 30);
  sim.runUntil(() => sim.isSnapshotComplete("snap-1"));
  return { sim, snapshot: sim.collectSnapshot("snap-1") };
}

test("acceptance: 3-node cluster, concurrent transfers, assets strictly conserved", () => {
  const { sim, snapshot } = runClusterSnapshot(42);

  // Every node recorded its local state and closed every incoming channel.
  assert.equal(snapshot.processes.length, 3);
  for (const record of snapshot.processes) {
    assert.equal(record.channels.length, 2);
  }
  assert.ok(sim.isSnapshotComplete("snap-1"));

  // Business never paused: transfers kept flowing after A fired the marker.
  assert.ok(sim.transfersSent > 0, "transfers happened");
  assert.ok(
    sim.transfersDeliveredAfterInitiation > 0,
    "transfers were still delivered while the snapshot was underway",
  );

  // The cut captured in-flight transfers on the channels.
  const inFlight = snapshot.processes.reduce((n, r) => n + inFlightTotal(r), 0);
  assert.ok(inFlight > 0, "channel states captured in-flight transfers");

  // Local states + in-flight messages == initial total assets. Strictly.
  assert.equal(snapshotTotal(snapshot), TOTAL);
});

test("conservation holds across many seeds and cluster sizes", () => {
  for (let seed = 1; seed <= 30; seed++) {
    const { snapshot } = runClusterSnapshot(seed);
    assert.equal(snapshotTotal(snapshot), TOTAL, `seed ${seed}`);
  }
  // 5-node cluster for good measure.
  const sim = new Simulation({
    balances: { A: 50, B: 70, C: 90, D: 110, E: 130 },
    seed: 7,
  });
  sim.scheduleSnapshotInitiation("C", "snap-x", 25);
  sim.runUntil(() => sim.isSnapshotComplete("snap-x"));
  assert.equal(snapshotTotal(sim.collectSnapshot("snap-x")), 450);
});

test("marker rule: first marker triggers local record + marker flood + channel recording", () => {
  const sent: Message[] = [];
  const b = new Process("B", 100, ["A", "C"]);
  b.send = (msg) => sent.push(msg);

  // A transfer from A arrives before any marker: just business.
  b.receive({ kind: "transfer", id: 1, from: "A", to: "B", amount: 10, sentAt: 0 }, 1);
  assert.equal(b.balance, 110);

  // First marker arrives from A at t=2: record local state, flood markers.
  b.receive({ kind: "marker", snapshotId: "s", from: "A", to: "B" }, 2);
  const markers = sent.filter((m) => m.kind === "marker");
  assert.deepEqual(
    markers.map((m) => (m as { to: string }).to).sort(),
    ["A", "C"],
    "marker sent on every outgoing channel",
  );

  // Channel A->B is closed (marker already seen); C->B is recording.
  const t1: TransferMessage = { kind: "transfer", id: 2, from: "C", to: "B", amount: 5, sentAt: 2 };
  const t2: TransferMessage = { kind: "transfer", id: 3, from: "A", to: "B", amount: 7, sentAt: 3 };
  b.receive(t1, 3);
  b.receive(t2, 4); // in transit on A->B BEFORE A recorded? No: after marker -> post-cut, not recorded
  b.receive({ kind: "marker", snapshotId: "s", from: "C", to: "B" }, 5);

  assert.ok(b.isSnapshotComplete("s"));
  const record = b.getSnapshot("s");
  assert.equal(record.balance, 110, "local state frozen at first marker");
  const fromC = record.channels.find((c) => c.from === "C")!;
  const fromA = record.channels.find((c) => c.from === "A")!;
  assert.deepEqual(fromC.inFlight, [t1], "transfer before C's marker is in-flight");
  assert.deepEqual(fromA.inFlight, [], "channel the first marker arrived on records empty");
});

test("in-flight transfer sent before sender's marker is captured in channel state", () => {
  // Manually wired A, B, C to control delivery order precisely.
  const wires = new Map<string, Message[]>();
  const make = (id: string) => {
    const p = new Process(id, 100, ["A", "B", "C"].filter((x) => x !== id));
    p.send = (msg) => wires.get(id)!.push(msg);
    return p;
  };
  const a = make("A"), b = make("B"), c = make("C");
  for (const id of ["A", "B", "C"]) wires.set(id, []);
  const take = (from: string, to: string, kind: Message["kind"]) => {
    const q = wires.get(from)!;
    const i = q.findIndex((m) => m.to === to && m.kind === kind);
    assert.ok(i >= 0, `expected ${kind} ${from}->${to}`);
    return q.splice(i, 1)[0];
  };

  // t=0: B wires 30 to C (still on the wire); A initiates the snapshot.
  b.sendTransfer(1, "C", 30, 0);
  a.initiateSnapshot("s", 0);
  // t=1,2: A's markers reach B and C; both record local state and flood markers.
  b.receive(take("A", "B", "marker"), 1);
  c.receive(take("A", "C", "marker"), 2);
  // t=3: B's pre-marker transfer finally lands at C -> in-flight on B->C.
  c.receive(take("B", "C", "transfer"), 3);
  // Remaining markers close every channel.
  a.receive(take("B", "A", "marker"), 4);
  a.receive(take("C", "A", "marker"), 4);
  b.receive(take("C", "B", "marker"), 4);
  c.receive(take("B", "C", "marker"), 4);

  for (const p of [a, b, c]) assert.ok(p.isSnapshotComplete("s"));
  const recordC = c.getSnapshot("s");
  const inFlight = recordC.channels.find((ch) => ch.from === "B")!.inFlight;
  assert.equal(inFlight.length, 1);
  assert.equal(inFlight[0].amount, 30);
  // Cut: A=100, B=70 (after sending), C=100 (before receiving) + 30 in-flight = 300.
  const total = a.getSnapshot("s").balance + b.getSnapshot("s").balance +
    recordC.balance + inFlightTotal(recordC);
  assert.equal(total, 300);
});

test("simulation channels are FIFO", () => {
  const sim = new Simulation({ balances: { A: 1000, B: 1000 }, seed: 9, channelDelay: [1, 10] });
  const a = sim.processes.get("A")!;
  const received: number[] = [];
  const b = sim.processes.get("B")!;
  const origReceive = b.receive.bind(b);
  b.receive = (msg, now) => {
    if (msg.kind === "transfer") received.push(msg.id);
    origReceive(msg, now);
  };
  for (let i = 1; i <= 20; i++) a.sendTransfer(i, "B", 1, 0);
  sim.runUntil(() => received.length === 20);
  assert.deepEqual(received, Array.from({ length: 20 }, (_, i) => i + 1));
});
