import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { writeFileSync, rmSync } from "node:fs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const cli = join(root, "src", "cli.ts");
const example = (name: string) => join(root, "examples", name);

function run(args: string[]): { code: number; stdout: string } {
  try {
    const stdout = execFileSync(process.execPath, [cli, ...args], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    return { code: 0, stdout };
  } catch (err) {
    const e = err as { status?: number; stdout?: string };
    return { code: e.status ?? 1, stdout: e.stdout ?? "" };
  }
}

test("CLI solve：最优实例输出分数结果", () => {
  const { code, stdout } = run(["solve", example("optimal.json")]);
  assert.equal(code, 0);
  const out = JSON.parse(stdout);
  assert.equal(out.status, "optimal");
  assert.equal(out.objective, "10");
  assert.ok(Array.isArray(out.x) && Array.isArray(out.y));
});

test("CLI solve + check：证书通过独立检查", () => {
  for (const name of ["optimal.json", "fractional.json", "infeasible.json", "unbounded.json", "degenerate.json", "beale.json"]) {
    const solved = run(["solve", example(name)]);
    assert.equal(solved.code, 0, name);
    const tmp = join(root, "tests", `.tmp-result-${name}`);
    writeFileSync(tmp, solved.stdout);
    const checked = run(["check", example(name), tmp]);
    assert.equal(checked.code, 0, `${name}: ${checked.stdout}`);
    assert.deepEqual(JSON.parse(checked.stdout), { ok: true, violations: [] });
    rmSync(tmp, { force: true });
  }
});

test("CLI：预算耗尽退出码为 3 且状态为 iteration_limit", () => {
  const { code, stdout } = run(["solve", example("optimal.json"), "--budget", "0"]);
  assert.equal(code, 3);
  assert.equal(JSON.parse(stdout).status, "iteration_limit");
});

test("CLI：从 stdin 读取问题", () => {
  const stdout = execFileSync(process.execPath, [cli, "solve", "-"], {
    encoding: "utf8",
    input: JSON.stringify({ c: ["1"], A: [["1"]], b: ["5"] }),
  });
  const out = JSON.parse(stdout);
  assert.equal(out.status, "optimal");
  assert.equal(out.objective, "5");
});
