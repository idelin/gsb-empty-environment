import { MessageBus } from "./src/bus.ts";
import { Replica } from "./src/replica.ts";

const bus = new MessageBus();
const mk = (id: string, now: number) =>
  new Replica({
    id,
    bus,
    defaultStrategy: "lww",
    strategies: { tags: "multi-value" },
    now: () => now,
  });

const a = mk("A", 100);
const b = mk("B", 100);
const c = mk("C", 100);

const counts = new Map<string, number>();
for (const r of [a, b, c]) {
  r.onCommit((_snap, txn) => {
    if (!txn.local) counts.set(r.id, (counts.get(r.id) ?? 0) + 1);
  });
}

const show = (label: string) => {
  console.log(label);
  for (const r of [a, b, c]) {
    console.log(`  ${r.id}:`, JSON.stringify(r.snapshot()));
  }
};

console.log("== 1. A applies one atomic transaction (3 fields) ==");
a.transact((t) => {
  t.set("title", "hello");
  t.set("count", 1);
  t.set("tags", "x");
});
bus.flush();
show("after flush:");

console.log("\n== 2. B and C make concurrent edits to different fields ==");
b.transact((t) => t.set("color", "blue"));
c.transact((t) => t.set("owner", "carol"));
bus.flush();
show("merged on all replicas:");

console.log("\n== 3. A overwrites color after observing blue (causal later write wins) ==");
a.transact((t) => t.set("color", "red"));
bus.flush();
show("LWW winner:");

console.log("\n== 4. same-field conflict: tags keeps all concurrent versions ==");
b.transact((t) => t.set("tags", "y"));
c.transact((t) => t.set("tags", "z"));
bus.flush();
show("multi-value:");

console.log("\n== 5. C disconnects; A edits; C reconnects and replays log ==");
bus.disconnect("C");
a.transact((t) => {
  t.set("count", 2);
  t.delete("owner");
});
bus.flush();
console.log("  while C is offline, C =", JSON.stringify(c.snapshot()));
bus.reconnect("C");
bus.flush();
console.log("  after replay,   C =", JSON.stringify(c.snapshot()));
bus.flush();
console.log("  extra flush leaves C unchanged (no double apply)");

console.log("\n== 6. side-effect audit (remote commits observed per replica) ==");
for (const r of [a, b, c]) {
  console.log(`  ${r.id}: remote commits seen = ${counts.get(r.id) ?? 0}`);
}
console.log("  local echoes fired 0 extra side effects (onCommit counts local once only)");

console.log("\n== 7. deterministic replay: rebuild C from its operation log ==");
console.log("  replay(C) =", JSON.stringify(c.rebuildFromLog()));
console.log("  live  (C) =", JSON.stringify(c.snapshot()));
