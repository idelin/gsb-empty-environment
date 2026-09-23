import { booleanLattice, powerSetLattice } from "../src/lattice.ts";
import {
  ComputationRing,
  NonMonotoneError,
  schedulers,
  type Scheduler,
} from "../src/system.ts";
import { Store } from "../src/store.ts";

function show(value: unknown): string {
  if (value instanceof Set) return `{${[...value].join(",")}}`;
  return String(value);
}

function printValues(values: ReadonlyMap<string, unknown>): void {
  console.log(
    `  values: ${[...values].map(([k, v]) => `${k}=${show(v)}`).join("  ")}`,
  );
}

function reachabilityRing(): ComputationRing {
  // graph: A -> B -> C -> A (cycle), C -> D; source: A
  return new ComputationRing()
    .declare("A", booleanLattice, ["C"], (c: boolean) => true || c)
    .declare("B", booleanLattice, ["A"], (a: boolean) => a)
    .declare("C", booleanLattice, ["B"], (b: boolean) => b)
    .declare("D", booleanLattice, ["C"], (c: boolean) => c);
}

console.log("== 1. boolean reachability closure over a cycle ==");
{
  const result = reachabilityRing().solve({ trace: true });
  for (const entry of result.trace) {
    console.log(`  ${entry.cell}: ${show(entry.from)} -> ${show(entry.to)}`);
  }
  console.log(`  status=${result.status} evaluations=${result.evaluations}`);
  printValues(result.values);
}

console.log("== 2. powerset ancestor closure ==");
{
  const nodes = ["A", "B", "C"];
  const lattice = powerSetLattice<string>(nodes);
  const ring = new ComputationRing()
    .declare("A", lattice, ["C"], (c: ReadonlySet<string>) => new Set(["A", ...c]))
    .declare("B", lattice, ["A"], (a: ReadonlySet<string>) => new Set(["B", ...a]))
    .declare("C", lattice, ["B"], (b: ReadonlySet<string>) => new Set(["C", ...b]));
  const result = ring.solve();
  console.log(`  status=${result.status} evaluations=${result.evaluations}`);
  printValues(result.values);
}

console.log("== 3. x = NOT x is rejected, no infinite spin ==");
{
  const ring = new ComputationRing().declare(
    "x",
    booleanLattice,
    ["x"],
    (x: boolean) => !x,
  );
  try {
    ring.solve();
    console.log("  unexpected: accepted");
  } catch (error) {
    if (error instanceof NonMonotoneError) {
      console.log(`  rejected: ${error.message}`);
    } else {
      throw error;
    }
  }
}

console.log("== 4. scheduling order does not change the least fixed point ==");
{
  const orders: [string, Scheduler][] = [
    ["fifo", schedulers.fifo],
    ["lifo", schedulers.lifo],
    ["random(7)", schedulers.random(7)],
    ["random(99)", schedulers.random(99)],
  ];
  for (const [name, scheduler] of orders) {
    const result = reachabilityRing().solve({ scheduler });
    console.log(
      `  ${name.padEnd(10)} status=${result.status} evaluations=${result.evaluations} ` +
        [...result.values].map(([k, v]) => `${k}=${show(v)}`).join(" "),
    );
  }
}

console.log("== 5. iteration budget exhaustion marks not-converged ==");
{
  const result = reachabilityRing().solve({ budget: 2 });
  console.log(
    `  status=${result.status} converged=${result.converged} evaluations=${result.evaluations}`,
  );
  printValues(result.values);
}

console.log("== 6. transactional store: isolation + replayable op log ==");
{
  const store = new Store();
  const before = store.snapshot();
  reachabilityRing().solve({ store });
  console.log(`  snapshot taken before solve sees A=${show(before.get("A"))}`);
  console.log(`  committed after one transaction (version ${store.currentVersion}):`);
  console.log(
    `    ${["A", "B", "C", "D"].map((k) => `${k}=${show(store.get(k))}`).join(" ")}`,
  );
  console.log(`  op log (${store.log.length} ops):`);
  for (const op of store.log) console.log(`    set ${op.key} = ${show(op.value)}`);
  const replayed = Store.replay(store.log);
  console.log(
    `  replayed from log: ${["A", "B", "C", "D"].map((k) => `${k}=${show(replayed.get(k))}`).join(" ")}`,
  );
}
