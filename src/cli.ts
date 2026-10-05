import { keyOf } from "./axial.ts";
import type { CellKey } from "./axial.ts";
import { auditStore } from "./audit.ts";
import { computeLedger, ledgerViolations } from "./ledger.ts";
import type { Ledger } from "./ledger.ts";
import { LeaseStore } from "./store.ts";
import type { OpResult } from "./model.ts";

function usage(): never {
  const lines = [
    "usage: hexlease <dir> <command> [args]",
    "",
    "  init --radius N --default-capacity C --partitions P",
    "  tick <n>",
    "  set-capacity <q> <r> <cap>",
    "  set-quota <tenant> <n|none>",
    "  create <tenant> <lease> --cells q:r,q:r [--expires t|--expires never] --request id [--tick t]",
    "  replace <tenant> <lease> --version v --cells q:r,q:r [--expires t|never|keep] --request id [--tick t]",
    "  release <tenant> <lease> --version v --request id [--tick t]",
    "  reassign <q> <r> <partition>",
    "  show <lease>",
    "  ledger [--all] [--json]",
    "  verify",
    "  pending",
    "  partitions",
  ];
  console.error(lines.join("\n"));
  process.exit(2);
}

function parseCells(spec: string): CellKey[] {
  if (!spec) return [];
  return spec.split(",").map((pair) => {
    const [q, r] = pair.split(":");
    return keyOf(Number(q), Number(r));
  });
}

function parseExpires(spec: string | undefined, fallback: number | null): number | null {
  if (spec === undefined) return fallback;
  if (spec === "never") return null;
  const n = Number(spec);
  if (!Number.isInteger(n)) throw new Error(`bad --expires value: ${spec}`);
  return n;
}

function flags(args: string[]): Map<string, string> {
  const out = new Map<string, string>();
  for (let i = 0; i < args.length; i++) {
    if (args[i].startsWith("--")) {
      const name = args[i].slice(2);
      const value = i + 1 < args.length && !args[i + 1].startsWith("--") ? args[++i] : "true";
      out.set(name, value);
    }
  }
  return out;
}

function printResult(result: OpResult): void {
  console.log(JSON.stringify(result));
  process.exitCode = result.ok ? 0 : 1;
}

function printLedger(ledger: Ledger, tick: number, all: boolean): void {
  console.log(`TICK ${tick}`);
  console.log("CELLS" + (all ? "" : " (usage>0 or over capacity)"));
  console.log("cell\tcapacity\tusage\tleases");
  for (const cell of ledger.cells) {
    if (!all && cell.usage === 0 && !cell.overCapacity) continue;
    const mark = cell.overCapacity ? "  OVER-CAPACITY (capacity reduced below usage)" : "";
    console.log(`${cell.cell}\t${cell.capacity}\t${cell.usage}\t${cell.leases.join(",")}${mark}`);
  }
  console.log("TENANTS");
  console.log("tenant\tquota\tusage\tleases");
  for (const tenant of ledger.tenants) {
    const quota = tenant.quota === null ? "inf" : String(tenant.quota);
    const mark = tenant.overQuota ? "  OVER-QUOTA" : "";
    console.log(`${tenant.tenantId}\t${quota}\t${tenant.usage}\t${tenant.leases.join(",")}${mark}`);
  }
  const violations = ledgerViolations(ledger);
  console.log(violations.length === 0 ? "SNAPSHOT VIOLATIONS: none" : `SNAPSHOT VIOLATIONS:\n  ${violations.join("\n  ")}`);
}

function main(argv: string[]): void {
  const [dir, cmd, ...rest] = argv;
  if (!dir || !cmd) usage();

  if (cmd === "init") {
    const f = flags(rest);
    LeaseStore.init(dir, {
      radius: Number(f.get("radius") ?? 3),
      defaultCapacity: Number(f.get("default-capacity") ?? 1),
      numPartitions: Number(f.get("partitions") ?? 4),
    });
    console.log(JSON.stringify({ ok: true, dir }));
    return;
  }

  if (cmd === "verify") {
    const report = auditStore(dir);
    console.log(
      `TRANSACTIONS committed=${report.committedTxs} aborted=${report.abortedTxs} orphanIntents=${report.orphanIntents}`,
    );
    const section = (name: string, items: string[]) =>
      items.length === 0 ? `${name}: none` : `${name}:\n  ${items.join("\n  ")}`;
    console.log(section("GRANT-TIME OVER-ALLOCATION", report.grantViolations));
    console.log(section("PARTITION LOG ERRORS", report.partitionErrors));
    console.log(section("SWEEP ERRORS", report.sweepErrors));
    const snapshot = ledgerViolations(report.ledger);
    if (snapshot.length === 0) {
      console.log("CURRENT SNAPSHOT: no cell over capacity, no tenant over quota");
    } else {
      console.log("CURRENT SNAPSHOT (only possible via capacity reduction, grandfathered):");
      for (const v of snapshot) console.log(`  ${v}`);
    }
    if (report.ok) {
      console.log("PROOF OK: every committed grant respected capacity and quota at commit time");
      return;
    }
    console.log("PROOF FAILED");
    process.exitCode = 1;
    return;
  }

  const store = LeaseStore.open(dir);
  try {
    switch (cmd) {
      case "tick": {
        printResult(store.tick(Number(rest[0])));
        return;
      }
      case "set-capacity": {
        const [q, r, cap] = rest;
        printResult(
          store.setCapacity({ cell: keyOf(Number(q), Number(r)), capacity: Number(cap) }, store.currentTick),
        );
        return;
      }
      case "set-quota": {
        const [tenant, quota] = rest;
        printResult(
          store.setQuota(
            { tenantId: tenant, quota: quota === "none" ? null : Number(quota) },
            store.currentTick,
          ),
        );
        return;
      }
      case "create": {
        const [tenant, leaseId] = rest;
        const f = flags(rest.slice(2));
        printResult(
          store.create(
            {
              tenantId: tenant,
              leaseId,
              cells: parseCells(f.get("cells") ?? ""),
              expiresAtTick: parseExpires(f.get("expires"), null),
              requestId: f.get("request"),
            },
            f.has("tick") ? Number(f.get("tick")) : store.currentTick,
          ),
        );
        return;
      }
      case "replace": {
        const [tenant, leaseId] = rest;
        const f = flags(rest.slice(2));
        const existing = store.getLease(leaseId);
        const expiresSpec = f.get("expires");
        printResult(
          store.replace(
            {
              tenantId: tenant,
              leaseId,
              expectedVersion: Number(f.get("version")),
              cells: parseCells(f.get("cells") ?? ""),
              expiresAtTick:
                expiresSpec === undefined || expiresSpec === "keep"
                  ? existing?.expiresAtTick ?? null
                  : parseExpires(expiresSpec, null),
              requestId: f.get("request"),
            },
            f.has("tick") ? Number(f.get("tick")) : store.currentTick,
          ),
        );
        return;
      }
      case "release": {
        const [tenant, leaseId] = rest;
        const f = flags(rest.slice(2));
        printResult(
          store.release(
            {
              tenantId: tenant,
              leaseId,
              expectedVersion: Number(f.get("version")),
              requestId: f.get("request"),
            },
            f.has("tick") ? Number(f.get("tick")) : store.currentTick,
          ),
        );
        return;
      }
      case "reassign": {
        const [q, r, partition] = rest;
        printResult(
          store.reassign(
            { cell: keyOf(Number(q), Number(r)), partition: Number(partition) },
            store.currentTick,
          ),
        );
        return;
      }
      case "show": {
        const lease = store.getLease(rest[0]);
        console.log(lease ? JSON.stringify(lease) : "not found");
        return;
      }
      case "ledger": {
        const f = flags(rest);
        const ledger = computeLedger(store.view());
        if (f.has("json")) {
          console.log(JSON.stringify({ tick: store.currentTick, ...ledger }, null, 2));
        } else {
          printLedger(ledger, store.currentTick, f.has("all"));
        }
        return;
      }
      case "pending": {
        const intents = store.pendingIntents();
        console.log(intents.length === 0 ? "no pending intents" : JSON.stringify(intents, null, 2));
        return;
      }
      case "partitions": {
        const meta = store.meta;
        const ownership = (store.dump() as { ownership: [string, number][] }).ownership;
        console.log(`numPartitions=${meta.numPartitions}`);
        for (const [cell, partition] of ownership) {
          console.log(`${cell} -> partition ${partition} (reassigned)`);
        }
        return;
      }
      default:
        usage();
    }
  } finally {
    store.close();
  }
}

main(process.argv.slice(2));
