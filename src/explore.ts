import { Op, InstrSpec, encodeProgram, decode } from "./isa";
import { analyze } from "./verify";
import { checkReference } from "./reference";

const MAX_STACK = 8;

function alphabet(len: number): InstrSpec[] {
  const specs: InstrSpec[] = [{ op: Op.Push }, { op: Op.Pop }, { op: Op.Add }, { op: Op.Ret }];
  for (let t = 0; t <= len; t++) {
    specs.push({ op: Op.Jmp, targetIndex: t });
    specs.push({ op: Op.Jz, targetIndex: t });
  }
  return specs;
}

function* enumeratePrograms(len: number): Generator<InstrSpec[]> {
  const alpha = alphabet(len);
  const current: InstrSpec[] = [];
  function* go(depth: number): Generator<InstrSpec[]> {
    if (depth === len) {
      yield current.slice();
      return;
    }
    for (const spec of alpha) {
      current.push(spec);
      yield* go(depth + 1);
      current.pop();
    }
  }
  yield* go(0);
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomProgram(rand: () => number, len: number): InstrSpec[] {
  const alpha = alphabet(len);
  const specs: InstrSpec[] = [];
  for (let i = 0; i < len; i++) {
    specs.push(alpha[Math.floor(rand() * alpha.length)]);
  }
  return specs;
}

interface Stats {
  total: number;
  valid: number;
  mismatches: number;
  validWithUnreachable: number;
  validWithUnreachableBadJump: number;
  validWithUnreachableUnderflow: number;
  validWithUnreachableBadRet: number;
  errorsByKind: Map<string, number>;
}

function process(specs: InstrSpec[], stats: Stats, mismatches: string[]): void {
  const code = encodeProgram(specs);
  const analysis = analyze(code, MAX_STACK);
  const ref = checkReference(code, MAX_STACK);
  stats.total++;

  const verifierOk = analysis.result.ok;
  if (verifierOk !== ref.ok) {
    stats.mismatches++;
    if (mismatches.length < 5) {
      mismatches.push(
        `program ${JSON.stringify(specs)}: verifier=${verifierOk ? "ok" : JSON.stringify(analysis.result)} ` +
          `reference=${ref.ok ? "ok" : ref.reason}`,
      );
    }
  }

  if (!verifierOk && !analysis.result.ok) {
    const kind = analysis.result.error.kind;
    stats.errorsByKind.set(kind, (stats.errorsByKind.get(kind) ?? 0) + 1);
    return;
  }
  if (!verifierOk) {
    return;
  }
  stats.valid++;

  const dec = decode(code);
  if (!dec.ok) {
    return;
  }
  let hasUnreachable = false;
  let hasBadJump = false;
  let hasUnderflow = false;
  let hasBadRet = false;
  for (const instr of dec.instrs) {
    if (analysis.reachable.has(instr.offset)) {
      continue;
    }
    hasUnreachable = true;
    if ((instr.op === Op.Jmp || instr.op === Op.Jz) && !dec.boundaries.has(instr.target!)) {
      hasBadJump = true;
    }
    if (instr.op === Op.Pop || instr.op === Op.Add) {
      hasUnderflow = true;
    }
    if (instr.op === Op.Ret) {
      hasBadRet = true;
    }
  }
  if (hasUnreachable) {
    stats.validWithUnreachable++;
  }
  if (hasBadJump) {
    stats.validWithUnreachableBadJump++;
  }
  if (hasUnderflow) {
    stats.validWithUnreachableUnderflow++;
  }
  if (hasBadRet) {
    stats.validWithUnreachableBadRet++;
  }
}

const stats: Stats = {
  total: 0,
  valid: 0,
  mismatches: 0,
  validWithUnreachable: 0,
  validWithUnreachableBadJump: 0,
  validWithUnreachableUnderflow: 0,
  validWithUnreachableBadRet: 0,
  errorsByKind: new Map<string, number>(),
};
const mismatches: string[] = [];

for (let len = 1; len <= 4; len++) {
  for (const specs of enumeratePrograms(len)) {
    process(specs, stats, mismatches);
  }
}

const rand = mulberry32(0x5eed);
for (let i = 0; i < 200000; i++) {
  const len = 5 + Math.floor(rand() * 4);
  process(randomProgram(rand, len), stats, mismatches);
}

console.log(`explored ${stats.total} programs (exhaustive length 1-4, sampled length 5-8)`);
console.log(`  valid: ${stats.valid}`);
console.log(`  verifier/reference mismatches: ${stats.mismatches}`);
for (const m of mismatches) {
  console.log(`    MISMATCH: ${m}`);
}
console.log(`  rejection reasons:`);
for (const [kind, count] of [...stats.errorsByKind.entries()].sort()) {
  console.log(`    ${kind}: ${count}`);
}
console.log(`  accepted programs containing unreachable instructions: ${stats.validWithUnreachable}`);
console.log(`    ...with unreachable jump to non-boundary: ${stats.validWithUnreachableBadJump}`);
console.log(`    ...with unreachable POP/ADD (would underflow at height 0): ${stats.validWithUnreachableUnderflow}`);
console.log(`    ...with unreachable RET (height unconstrained): ${stats.validWithUnreachableBadRet}`);

if (stats.mismatches > 0) {
  throw new Error("verifier disagrees with reference exploration");
}
if (stats.validWithUnreachableBadJump === 0 || stats.validWithUnreachableUnderflow === 0) {
  throw new Error("exploration did not cover unreachable-bad-code cases");
}
console.log("OK: verifier agrees with reference on all explored programs; unreachable bad code never reported");
