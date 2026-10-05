import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
const CLI = join(ROOT, "bin", "hexlease");

function run(dir: string, ...args: string[]) {
  const proc = spawnSync("bash", [CLI, dir, ...args], { encoding: "utf8" });
  return { status: proc.status, stdout: proc.stdout.trim(), stderr: proc.stderr.trim() };
}

test("CLI end-to-end: init, lease lifecycle, ledger, verify", () => {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-cli-"));

  let r = run(dir, "init", "--radius", "2", "--default-capacity", "1", "--partitions", "3");
  assert.equal(r.status, 0, r.stderr);

  r = run(dir, "set-quota", "acme", "3");
  assert.equal(r.status, 0);
  assert.deepEqual(JSON.parse(r.stdout), { ok: true, tenantId: "acme", quota: 3 });

  r = run(dir, "create", "acme", "L1", "--cells", "0:0,1:0", "--expires", "10", "--request", "r1");
  assert.equal(r.status, 0);
  assert.deepEqual(JSON.parse(r.stdout), { ok: true, leaseId: "L1", version: 1 });

  // idempotent replay through the CLI
  const replay = run(dir, "create", "acme", "L1", "--cells", "0:0,1:0", "--expires", "10", "--request", "r1");
  assert.equal(replay.status, 0);
  assert.deepEqual(JSON.parse(replay.stdout), { ok: true, leaseId: "L1", version: 1 });

  // conflicting content with the same request id
  const conflict = run(dir, "create", "acme", "L1", "--cells", "0:0", "--expires", "10", "--request", "r1");
  assert.equal(conflict.status, 1);
  assert.equal(JSON.parse(conflict.stdout).error, "REQUEST_CONFLICT");

  // replace across the map (net change checked as one transaction)
  r = run(dir, "replace", "acme", "L1", "--version", "1", "--cells", "1:0,-1:0,0:1", "--expires", "keep", "--request", "r2");
  assert.equal(r.status, 0);
  assert.deepEqual(JSON.parse(r.stdout), { ok: true, leaseId: "L1", version: 2 });

  // version mismatch cannot modify
  const stale = run(dir, "replace", "acme", "L1", "--version", "1", "--cells", "0:0", "--request", "r3");
  assert.equal(stale.status, 1);
  assert.equal(JSON.parse(stale.stdout).error, "VERSION_MISMATCH");

  // expiry is processed before same-tick requests
  r = run(dir, "tick", "10");
  assert.equal(r.status, 0);
  assert.deepEqual(JSON.parse(r.stdout).expired, ["L1"]);
  r = run(dir, "show", "L1");
  assert.equal(r.stdout, "not found");

  // capacity reduction below usage is grandfathered, not a grant violation
  run(dir, "set-capacity", "0", "0", "2");
  run(dir, "create", "acme", "A", "--cells", "0:0", "--request", "ra");
  run(dir, "create", "acme", "B", "--cells", "0:0", "--request", "rb");
  r = run(dir, "set-capacity", "0", "0", "1");
  assert.equal(r.status, 0);

  r = run(dir, "ledger");
  assert.equal(r.status, 0);
  assert.match(r.stdout, /TICK 10/);
  assert.match(r.stdout, /OVER-CAPACITY \(capacity reduced below usage\)/);
  assert.match(r.stdout, /acme\t3\t2/);

  r = run(dir, "verify");
  assert.equal(r.status, 0, r.stdout);
  assert.match(r.stdout, /GRANT-TIME OVER-ALLOCATION: none/);
  assert.match(r.stdout, /PARTITION LOG ERRORS: none/);
  assert.match(r.stdout, /PROOF OK/);

  // releasing one lease resolves the oversubscription
  run(dir, "release", "acme", "A", "--version", "1", "--request", "rel-a");
  r = run(dir, "ledger");
  assert.doesNotMatch(r.stdout, /OVER-CAPACITY/);
  r = run(dir, "verify");
  assert.equal(r.status, 0);
});

test("CLI: quota enforced across processes", () => {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-cli-"));
  run(dir, "init", "--radius", "1", "--default-capacity", "3", "--partitions", "2");
  run(dir, "set-quota", "acme", "1");
  let r = run(dir, "create", "acme", "L1", "--cells", "0:0", "--request", "q1");
  assert.equal(r.status, 0);
  r = run(dir, "create", "acme", "L2", "--cells", "1:0", "--request", "q2");
  assert.equal(r.status, 1);
  assert.equal(JSON.parse(r.stdout).error, "TENANT_QUOTA_EXCEEDED");
});
