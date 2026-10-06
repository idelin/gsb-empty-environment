import { test } from "node:test";
import assert from "node:assert/strict";
import { Op } from "../src/isa.ts";
import { verify } from "../src/verifier.ts";
import { enumeratePrograms, randomPrograms, checkProgram } from "../src/explore.ts";

function runAll(programs: Iterable<Uint8Array>, label: string): void {
  let total = 0;
  let accepted = 0;
  let truncated = 0;
  const failures: string[] = [];
  for (const code of programs) {
    total++;
    const { failure, truncated: t } = checkProgram(code);
    if (t) truncated++;
    if (verify(code).ok) accepted++;
    if (failure) {
      failures.push(`${label} program [${[...code].join(" ")}]: ${failure}`);
      if (failures.length >= 5) break;
    }
  }
  assert.equal(failures.length, 0, failures.join("\n"));
  // A few accepted programs loop with ever-growing values (constant height),
  // which legitimately exhausts concrete exploration bounds.
  assert.equal(truncated <= Math.ceil(total * 0.01), true, `${truncated}/${total} explorations truncated`);
  console.log(`  ${label}: ${total} programs, ${accepted} accepted, ${total - accepted} rejected, 0 disagreements`);
}

test("exhaustive: all programs of <=3 instructions agree with concrete exploration", () => {
  runAll(enumeratePrograms(3, 8), "exhaustive");
});

test("random: 20k seeded programs agree with concrete exploration", () => {
  runAll(randomPrograms(42, 20_000, 10), "random");
});

test("appending unreachable bad code never changes an accept verdict", () => {
  // Bad-code suffixes placed after RET are unreachable; the verifier must
  // keep accepting (structural jump errors excluded by using no jumps here).
  const suffixes: number[][] = [
    [Op.POP], // underflow
    [Op.ADD], // underflow needing 2
    [Op.PUSH, 1, Op.PUSH, 2, Op.RET], // bad return height
    [Op.PUSH, 1, Op.JMP, 0], // would be a growing loop if reached (target 0 is a boundary)
    [Op.RET], // RET with height 0
    [Op.POP, Op.POP, Op.ADD],
  ];
  const safePrefixes: number[][] = [
    [Op.PUSH, 1, Op.RET],
    [Op.PUSH, 1, Op.PUSH, 2, Op.ADD, Op.RET],
    [Op.PUSH, 0, Op.POP, Op.PUSH, 3, Op.RET],
  ];
  let checked = 0;
  for (const prefix of safePrefixes) {
    for (const suffix of suffixes) {
      // The prefix ends in RET, so the suffix is unreachable.
      const code = Uint8Array.from([...prefix, ...suffix]);
      const r = verify(code);
      assert.equal(
        r.ok,
        true,
        `false positive: dead suffix [${suffix.join(" ")}] after [${prefix.join(" ")}]`,
      );
      checked++;
    }
  }
  assert.equal(checked > 0, true);
  console.log(`  dead-code suffixes: ${checked} accepted programs with unreachable bad tails`);
});
