import { MigrationEngine, type PublishRecord } from "../src/engine.ts";
import { MigrationError } from "../src/errors.ts";
import { VersionGraph, type MigrationStep } from "../src/graph.ts";
import { MemoryJournalStore } from "../src/journal.ts";
import type { JsonObject, JsonValue } from "../src/json.ts";

const asObj = (v: JsonValue): JsonObject => v as JsonObject;

/* ---------- a realistic version graph ---------- */

const v1Schema = {
  type: "object",
  properties: {
    fullname: { type: "string" },
    address: { type: "string" },
  },
  required: ["fullname", "address"],
} as const;

const v11Schema = {
  type: "object",
  properties: {
    name: { type: "string" },
    address: { type: "string" },
    history: { type: "array", items: { type: "string" } },
  },
  required: ["name", "address", "history"],
} as const;

const v2Schema = {
  type: "object",
  properties: {
    name: { type: "string" },
    address: {
      type: "object",
      properties: { city: { type: "string" }, zip: { type: "string" } },
      required: ["city", "zip"],
    },
    history: { type: "array", items: { type: "string" } },
  },
  required: ["name", "address", "history"],
} as const;

function makeSteps(crashOnce: { armed: boolean }): MigrationStep[] {
  return [
    {
      name: "rename-fullname",
      from: "1.0.0",
      to: "1.1.0",
      inputSchema: v1Schema,
      outputSchema: v11Schema,
      apply(doc) {
        const d = asObj(doc);
        return {
          name: d.fullname,
          address: d.address,
          history: ["1.1.0"], // appended field: must never be duplicated
        };
      },
    },
    {
      name: "split-address",
      from: "1.1.0",
      to: "2.0.0",
      inputSchema: v11Schema,
      outputSchema: v2Schema,
      apply(doc) {
        if (crashOnce.armed) {
          crashOnce.armed = false;
          throw new Error("simulated crash (disk full)");
        }
        const d = asObj(doc);
        const [city, zip] = String(d.address).split("|");
        return {
          name: d.name,
          address: { city, zip },
          history: [...(d.history as JsonValue[]), "2.0.0"],
        };
      },
    },
    {
      name: "direct-1-to-2",
      from: "1.0.0",
      to: "2.0.0",
      cost: 5, // lower priority: only used if the 2-hop route is absent
      inputSchema: v1Schema,
      outputSchema: v2Schema,
      apply(doc) {
        const d = asObj(doc);
        const [city, zip] = String(d.address).split("|");
        return {
          name: d.fullname,
          address: { city, zip },
          history: ["2.0.0"],
        };
      },
    },
  ];
}

/* ---------- demo helpers ---------- */

const engine = new MigrationEngine();
const published: PublishRecord[] = [];
const publish = (r: PublishRecord) => published.push(r);

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function showError(e: unknown): void {
  if (e instanceof MigrationError) {
    console.log("rejected:", e.describe());
    for (const issue of e.issues ?? []) {
      console.log(`  issue at ${issue.path}: ${issue.message}`);
    }
  } else {
    throw e;
  }
}

/* ---------- 1. deterministic route + publish on success ---------- */

section("1. Deterministic route 1.0.0 -> 2.0.0 (2-hop beats direct cost 5)");
const crashOnce = { armed: false };
const graph = new VersionGraph();
for (const s of makeSteps(crashOnce)) graph.addStep(s);
const route = graph.findPath("1.0.0", "2.0.0");
console.log(
  `route: ${route.steps.map((s) => s.name).join(" -> ")} (cost ${route.totalCost})`,
);
const input: JsonValue = { address: "Shanghai|200000", fullname: "Ada Lovelace" };
const ok = engine.migrate({ graph, from: "1.0.0", to: "2.0.0", input, publish });
console.log("result:", JSON.stringify(ok.data));
console.log("published versions:", published.map((p) => p.version).join(", "));

/* ---------- 2. ambiguity is rejected ---------- */

section("2. Two equal-cost routes -> ambiguity rejected");
const ambiguous = new VersionGraph();
const mk = (name: string, via: string): MigrationStep[] => [
  {
    name: `${name}-a`,
    from: "A",
    to: via,
    inputSchema: { type: "any" },
    outputSchema: { type: "any" },
    apply: (d) => d,
  },
  {
    name: `${name}-b`,
    from: via,
    to: "B",
    inputSchema: { type: "any" },
    outputSchema: { type: "any" },
    apply: (d) => d,
  },
];
for (const s of [...mk("route1", "X"), ...mk("route2", "Y")]) ambiguous.addStep(s);
try {
  ambiguous.findPath("A", "B");
  console.log("ERROR: ambiguity was not detected");
} catch (e) {
  showError(e);
}

/* ---------- 3. schema errors locate the exact path ---------- */

section("3. Input schema violation is located to a concrete path");
try {
  engine.migrate({
    graph,
    from: "1.0.0",
    to: "2.0.0",
    input: { fullname: 42, extra: true },
  });
} catch (e) {
  showError(e);
}
console.log("published so far (unchanged):", published.length);

/* ---------- 4. crash, checkpoint, resume without duplication ---------- */

section("4. Crash mid-migration, then resume from checkpoint");
const crashGraph = new VersionGraph();
const crashFlag = { armed: true };
for (const s of makeSteps(crashFlag)) crashGraph.addStep(s);
const journal = new MemoryJournalStore();
const doc: JsonValue = { fullname: "Grace Hopper", address: "NYC|10001" };

try {
  engine.migrate({ graph: crashGraph, from: "1.0.0", to: "2.0.0", input: doc, journal, publish });
} catch (e) {
  showError(e);
}
console.log("journal after crash:", JSON.stringify(journal.load()));

const resumed = engine.migrate({
  graph: crashGraph,
  from: "1.0.0",
  to: "2.0.0",
  input: doc, // same original input (key order may differ)
  journal,
  publish,
});
console.log("resumed. skipped steps:", resumed.skippedSteps.join(", "));
console.log("executed steps:", resumed.executedSteps.join(", "));
console.log("history (no duplicates):", JSON.stringify(asObj(resumed.data).history));

/* ---------- 5. tampered input on resume is rejected ---------- */

section("5. Resume with a different original input is rejected");
const crashFlag2 = { armed: true };
const crashGraph2 = new VersionGraph();
for (const s of makeSteps(crashFlag2)) crashGraph2.addStep(s);
const journal2 = new MemoryJournalStore();
try {
  engine.migrate({ graph: crashGraph2, from: "1.0.0", to: "2.0.0", input: doc, journal: journal2 });
} catch {
  /* crashed as designed */
}
try {
  engine.migrate({
    graph: crashGraph2,
    from: "1.0.0",
    to: "2.0.0",
    input: { fullname: "Mallory", address: "NYC|10001" }, // tampered!
    journal: journal2,
  });
} catch (e) {
  showError(e);
}

/* ---------- 6. failed migration publishes nothing ---------- */

section("6. Publish only happens on success");
console.log(`total published records: ${published.length}`);
for (const p of published) console.log(` - ${p.version}: ${JSON.stringify(p.data)}`);
