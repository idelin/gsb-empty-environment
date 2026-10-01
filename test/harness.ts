// Test harness: uses Node's built-in node:test runner when available
// (Node >= 18), otherwise falls back to a minimal built-in runner so the
// same suite still executes on older Node versions.

import { createRequire } from "node:module";

declare const process: { exitCode: number };

type TestFn = () => unknown;

interface TestEntry {
  name: string;
  fn: TestFn;
}

const req: any = createRequire(import.meta.url);
let nodeTest: any = null;
try {
  nodeTest = req("node:test");
} catch {
  nodeTest = null;
}

const entries: TestEntry[] = [];

function fallbackTest(name: string, fn: TestFn): void {
  entries.push({ name: name, fn: fn });
}

export const test: any = nodeTest !== null ? nodeTest.test : fallbackTest;

// Executes registered tests when node:test is unavailable; otherwise a no-op
// (node:test runs registered tests automatically).
export async function runFallback(): Promise<void> {
  if (nodeTest !== null) {
    return;
  }
  let passed: number = 0;
  let failed: number = 0;
  for (const entry of entries) {
    try {
      await entry.fn();
      passed += 1;
      console.log("ok " + (passed + failed) + " - " + entry.name);
    } catch (err) {
      failed += 1;
      console.error("not ok " + (passed + failed) + " - " + entry.name);
      console.error(err);
    }
  }
  console.log("# tests " + (passed + failed) + " | pass " + passed + " | fail " + failed);
  if (failed > 0) {
    process.exitCode = 1;
  }
}
