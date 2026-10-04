import { afterAll, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { CHUNK_SIZE, makeBytes } from "./helpers";

const dirs: string[] = [];
afterAll(async () => {
  for (const d of dirs) await rm(d, { recursive: true, force: true });
});

function cli(args: string[], cwd: string) {
  return spawnSync(process.execPath, ["src/cli.ts", ...args], {
    cwd: join(import.meta.dir, ".."),
    encoding: "utf8",
  });
}

describe("cli", () => {
  test("commit -> prove -> verify round trip, and tamper detection", async () => {
    const dir = await mkdtemp(join(tmpdir(), "cfv-cli-"));
    dirs.push(dir);
    const file = join(dir, "data.bin");
    await writeFile(file, makeBytes(10 * CHUNK_SIZE + 3, 81));

    const commit = cli(["commit", file, "--chunk-size", String(CHUNK_SIZE)], dir);
    expect(commit.status).toBe(0);
    const commitment = JSON.parse(commit.stdout);
    expect(commitment.fileLength).toBe(10 * CHUNK_SIZE + 3);
    await writeFile(join(dir, "commitment.json"), commit.stdout);

    const prove = cli(
      ["prove", file, "--chunk-size", String(CHUNK_SIZE), "--range", "3:6"],
      dir,
    );
    expect(prove.status).toBe(0);
    await writeFile(join(dir, "bundle.json"), prove.stdout);

    const verify = cli(
      ["verify", join(dir, "commitment.json"), join(dir, "bundle.json"), "--range", "3:6"],
      dir,
    );
    expect(verify.status).toBe(0);
    const report = JSON.parse(verify.stdout);
    expect(report.commitmentValid).toBe(true);
    expect(report.ok).toEqual([3, 4, 5]);
    expect(report.bad).toEqual([]);

    // Tamper with one chunk inside the bundle: verification must fail loudly.
    const bundle = JSON.parse(prove.stdout);
    const raw = Buffer.from(bundle.chunks["4"], "base64");
    raw[0] ^= 0xff;
    bundle.chunks["4"] = raw.toString("base64");
    await writeFile(join(dir, "bundle.json"), JSON.stringify(bundle));

    const verify2 = cli(
      ["verify", join(dir, "commitment.json"), join(dir, "bundle.json"), "--range", "3:6"],
      dir,
    );
    expect(verify2.status).toBe(1);
    const report2 = JSON.parse(verify2.stdout);
    expect(report2.bad).toEqual([4]);
    expect(report2.ok).toEqual([3, 5]);
  });
});
