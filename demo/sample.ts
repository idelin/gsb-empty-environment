/**
 * Acceptance scenario:
 *   Pattern: A -> B within 10s, no C in between.
 *   t=0 A, t=5 B  => match emitted.
 *   Late event: C at t=2 arrives afterwards => previous match is precisely
 *   retracted and the retraction is written to the journal.
 */
import { CepEngine } from "../src/index.ts";
import type { CepEvent, Notification } from "../src/index.ts";

const engine = new CepEngine();

engine.registerPattern({
  name: "A-then-B-no-C",
  sequence: ["A", "B"],
  forbid: ["C"],
  windowMs: 10_000,
});

engine.subscribe((n: Notification) => {
  if (n.kind === "match") {
    console.log(
      "MATCH   #" + n.matchId + " [" + n.pattern + "] " +
        n.events.map((e) => e.type + "@t=" + e.timestamp / 1000 + "s").join(" -> "),
    );
  } else {
    console.log("RETRACT #" + n.matchId + " [" + n.pattern + "] reason: " + n.reason);
  }
});

const ev = (type: string, tSec: number): CepEvent => ({
  id: type + "@" + tSec,
  type,
  timestamp: tSec * 1000,
});

console.log("--- ingest A@t=0s ---");
engine.ingest(ev("A", 0));

console.log("--- ingest B@t=5s ---");
engine.ingest(ev("B", 5));

console.log("--- late arrival: C@t=2s (out-of-order compensation) ---");
engine.ingest(ev("C", 2));

console.log("--- advance watermark to t=20s (timeout sweep) ---");
engine.advanceTo(20_000);

console.log("");
console.log("=== journal ===");
for (const j of engine.journal) {
  console.log("[at=" + j.at + "ms] " + j.kind.padEnd(12) + " " + j.pattern + ": " + j.message);
}
