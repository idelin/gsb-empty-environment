/**
 * End-to-end demo:
 *  1. Build a nested protobuf message by hand, encode it, decode it back
 *     through the reflection engine, and show truncation safety.
 *  2. Run a multi-tenant workload through the DRR fair scheduler and print
 *     per-tenant service shares.
 */
import { decodeMessage, type MessageSchema } from "../src/wire/schema.ts";
import { encodeMessage } from "../src/wire/encoder.ts";
import { WireError } from "../src/wire/errors.ts";
import { TenantScheduler } from "../src/scheduler/scheduler.ts";
import assert from "node:assert/strict";

// ---------- Part 1: wire codec ----------
const Order: MessageSchema = {
  name: "Order",
  fields: {
    1: { name: "id", type: "zigzag64" },
    2: { name: "sku", type: "string" },
    3: { name: "quantities", type: "zigzag32", repeated: true, packed: true },
    4: { name: "price", type: "fixed64" },
    5: {
      name: "customer",
      type: "message",
      message: {
        name: "Customer",
        fields: {
          1: { name: "name", type: "string" },
          2: { name: "vip", type: "bool" },
          3: {
            name: "address",
            type: "message",
            message: {
              name: "Address",
              fields: {
                1: { name: "city", type: "string" },
                2: { name: "zip", type: "fixed32" },
              },
            },
          },
        },
      },
    },
  },
};

const order = {
  id: -9007199254740991n,
  sku: "WIDGET-零依赖",
  quantities: [3, -1, 2700],
  price: 1999n,
  customer: { name: "ada", vip: true, address: { city: "Shanghai", zip: 200000 } },
};

const buf = encodeMessage(order, Order);
console.log(`encoded ${buf.length} bytes:`, Buffer.from(buf).toString("hex"));
const decoded = decodeMessage(buf, Order);
console.log("decoded:", JSON.stringify(decoded, (_, v) => (typeof v === "bigint" ? v.toString() : v)));
assert.deepEqual(decoded, order);
console.log("roundtrip equal: true");

// Truncation safety: chop the stream and watch the guard fire.
const cut = buf.subarray(0, buf.length - 5);
try {
  decodeMessage(cut, Order);
  console.log("truncated input decoded (prefix was still well-formed)");
} catch (e) {
  const we = e as WireError;
  console.log(`truncation guarded: kind=${we.kind} offset=${we.offset} :: ${we.message}`);
}

// ---------- Part 2: fair scheduler ----------
const sched = new TenantScheduler({ maxConcurrentTotal: 4 });
sched.registerTenant("analytics", { weight: 3, maxConcurrent: 3 });
sched.registerTenant("billing", { weight: 2, maxConcurrent: 2 });
sched.registerTenant("guest", { weight: 1, maxConcurrent: 1, maxQueue: 100 });

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const jobs: Promise<unknown>[] = [];
const dispatchLog: string[] = [];
const submit = (tenant: string, n: number, ms: number) => {
  for (let i = 0; i < n; i++) {
    jobs.push(
      sched.submit(tenant, {
        run: async () => {
          dispatchLog.push(tenant[0]); // A/B/G
          await sleep(ms);
        },
      }),
    );
  }
};
submit("analytics", 30, 20);
submit("billing", 30, 20);
submit("guest", 30, 20);

await Promise.all(jobs);
await sched.drain();
console.log("\ndispatch order (A=nalytics w3, B=illing w2, G=uest w1):");
console.log("  " + dispatchLog.slice(0, 36).join(" "));
console.log("\nscheduler drained, idle =", sched.idle, ", totalInFlight =", sched.totalInFlight);
for (const s of sched.stats()) {
  console.log(
    `  ${s.id.padEnd(10)} weight=${s.weight} dispatched=${s.dispatched} completed=${s.completed} inFlight=${s.inFlight} queued=${s.queued}`,
  );
}
