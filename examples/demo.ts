import { CdcLog } from "../src/cdc-log.ts";
import { RollbackEngine } from "../src/engine.ts";
import { TableStore } from "../src/store.ts";
import type { RowImage } from "../src/types.ts";

// --- Scenario: a mistaken UPDATE wipes 1000 user profiles -------------------

const log = new CdcLog();
const users = new TableStore("users", { log });
const engine = new RollbackEngine(log);
engine.registerStore(users);

// 1. Seed 1000 users.
users.beginTx();
for (let i = 1; i <= 1000; i++) {
  users.insert({
    id: i,
    name: `user-${i}`,
    email: `user${i}@example.com`,
    phone: `138${String(i).padStart(8, "0")}`,
    balance: i * 10,
  });
}
users.commitTx();
const snapshot: RowImage[] = users.allRows();
console.log(`[setup] seeded ${users.size} users, cdc events: ${log.size}`);

// 2. Operator runs UPDATE ... without WHERE: every profile is cleared.
const badTx = users.beginTx();
const wiped = users.updateWhere(
  () => true,
  { name: "", email: "", phone: "", balance: 0 },
);
users.commitTx();
console.log(`[incident] tx ${badTx} wiped ${wiped} rows (name/email/phone/balance)`);
console.log(`[incident] sample row after wipe: ${JSON.stringify(users.get(1))}`);

// 3. Engine reads before images from the CDC log and builds a rollback package.
const pkg = engine.buildPackageForTx(badTx, "restore users wiped by " + badTx);
console.log(`[rollback] package ${pkg.id}: ${pkg.ops.length} compensation ops`);
console.log(`[rollback] first 3 reverse SQL statements:`);
for (const op of pkg.ops.slice(0, 3)) console.log(`           ${op.sql}`);

// 4. Replay the package: 1000 reverse UPDATEs restore the original rows.
const started = performance.now();
const result = engine.replay(pkg);
const elapsed = (performance.now() - started).toFixed(1);
console.log(`[rollback] replayed ${result.applied} ops in ${elapsed} ms`);

// 5. Verify byte-for-byte restoration of the pre-incident state.
const restored = users.allRows();
const ok =
  restored.length === snapshot.length &&
  restored.every((row, i) => JSON.stringify(row) === JSON.stringify(snapshot[i]));
console.log(`[verify] restored state identical to pre-incident snapshot: ${ok}`);
console.log(`[verify] sample row after restore: ${JSON.stringify(users.get(1))}`);
if (!ok) process.exit(1);
