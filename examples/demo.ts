import { QuotaService } from "../src/service.ts";

function show(label: string, fn: () => unknown): void {
  try {
    console.log(`${label}:`, fn());
  } catch (err) {
    console.log(`${label}: [${(err as Error).name}] ${(err as Error).message}`);
  }
}

const svc = new QuotaService(
  { userCredits: 10, gpuHours: 4 },
  {
    version: 1,
    condition: {
      op: "and",
      conditions: [
        { op: "in", attr: "tier", set: ["pro", "enterprise"] },
        { op: "gte", attr: "ageDays", value: 30 },
      ],
    },
  },
);

const attrs = { tier: "pro", ageDays: 90 } as const;
const state = () => svc.snapshot();

console.log("--- 1. atomic multi-dimension deduction ---");
show("acquire op-1 {userCredits:3, gpuHours:1}", () => {
  const p = svc.acquire({ operationId: "op-1", requirements: { userCredits: 3, gpuHours: 1 }, attributes: attrs, policyVersion: 1 });
  return { permit: p, state: state() };
});

console.log("\n--- 2. user quota fits but resource quota does not: nothing is deducted ---");
show("acquire op-2 {userCredits:6, gpuHours:5}", () =>
  svc.acquire({ operationId: "op-2", requirements: { userCredits: 6, gpuHours: 5 }, attributes: attrs, policyVersion: 1 }));
console.log("state after failure:", state());

console.log("\n--- 3. idempotent retry: same id, same requirements => deducted once ---");
svc.acquire({ operationId: "op-3", requirements: { userCredits: 4 }, attributes: attrs, policyVersion: 1 });
show("retry op-3", () => svc.acquire({ operationId: "op-3", requirements: { userCredits: 4 }, attributes: attrs, policyVersion: 1 }));
console.log("state:", state());

console.log("\n--- 4. same id, different requirements => conflict ---");
show("op-3 with {userCredits:5}", () =>
  svc.acquire({ operationId: "op-3", requirements: { userCredits: 5 }, attributes: attrs, policyVersion: 1 }));

console.log("\n--- 5. cancel rolls back every dimension ---");
svc.acquire({ operationId: "op-4", requirements: { userCredits: 2, gpuHours: 2 }, attributes: attrs, policyVersion: 1 });
console.log("before cancel:", state());
show("cancel op-4", () => svc.cancel("op-4"));
console.log("after cancel:", state());

console.log("\n--- 6. failure rolls back every dimension ---");
svc.acquire({ operationId: "op-5", requirements: { gpuHours: 1 }, attributes: attrs, policyVersion: 1 });
show("fail op-5", () => svc.fail("op-5"));
console.log("after fail:", state());

console.log("\n--- 7. policy change bumps version; old permits are rejected ---");
svc.setPolicy({ version: 2, condition: { op: "in", attr: "tier", set: ["enterprise"] } });
show("op-6 at stale version 1", () =>
  svc.acquire({ operationId: "op-6", requirements: { userCredits: 1 }, attributes: { tier: "enterprise" }, policyVersion: 1 }));
show("op-6 at version 2, tier=pro (policy now requires enterprise)", () =>
  svc.acquire({ operationId: "op-6", requirements: { userCredits: 1 }, attributes: { tier: "pro" }, policyVersion: 2 }));
show("op-6 at version 2, tier=enterprise", () =>
  svc.acquire({ operationId: "op-6", requirements: { userCredits: 1 }, attributes: { tier: "enterprise" }, policyVersion: 2 }));
console.log("final state:", state());
