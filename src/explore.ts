import { Op, decode } from "./isa.ts";
import { verify, replayPath, type VerifyError } from "./verifier.ts";

// ---------------------------------------------------------------------------
// Concrete state-space exploration.
//
// BFS over concrete (pc, stack) configurations from (0, []). The verifier is
// value-insensitive (it explores both JZ successors), so it over-approximates
// concrete reachability. The precise relationship is:
//   - concrete violation  => verifier must reject      (no false negatives)
//   - verifier accepts     => concrete finds nothing    (accepted code is safe;
//                                                          dead bad code is not reported)
//   - verifier rejects     => evidence paths must replay as valid *structural*
//                             paths with matching heights (they may be
//                             value-infeasible; that is standard JVM-style
//                             over-approximation, not a bug)
// ---------------------------------------------------------------------------

export interface ConcreteViolation {
  kind: "underflow" | "ret-height" | "height-conflict";
  pc: number;
  detail: string;
}

export interface ExploreResult {
  violations: ConcreteViolation[];
  truncated: boolean;
  statesExplored: number;
}

export function exploreConcrete(
  code: Uint8Array,
  opts: { maxStates?: number; maxHeight?: number } = {},
): ExploreResult {
  const maxStates = opts.maxStates ?? 50_000;
  const maxHeight = opts.maxHeight ?? 64;

  const decoded = decode(code);
  if (!decoded.ok) return { violations: [], truncated: false, statesExplored: 0 };
  const { byOffset } = decoded;

  const violations: ConcreteViolation[] = [];
  const heightsAt = new Map<number, Set<number>>();
  const visited = new Set<string>();
  const queue: Array<{ pc: number; stack: number[] }> = [{ pc: 0, stack: [] }];
  let truncated = false;

  const pushState = (pc: number, stack: number[]): void => {
    if (stack.length > maxHeight) {
      truncated = true;
      return;
    }
    const key = `${pc}|${stack.join(",")}`;
    if (visited.has(key)) return;
    visited.add(key);
    if (visited.size > maxStates) {
      truncated = true;
      return;
    }
    queue.push({ pc, stack });
  };

  while (queue.length > 0) {
    const { pc, stack } = queue.shift()!;
    const instr = byOffset.get(pc);
    if (!instr) continue; // fell off the end: path ends

    const seen = heightsAt.get(pc) ?? new Set<number>();
    if (!seen.has(stack.length) && seen.size > 0) {
      violations.push({
        kind: "height-conflict",
        pc,
        detail: `heights ${[...seen, stack.length].join(" vs ")}`,
      });
    }
    seen.add(stack.length);
    heightsAt.set(pc, seen);

    const fallthrough = pc + instr.size;
    switch (instr.op) {
      case Op.PUSH:
        pushState(fallthrough, [...stack, instr.arg!]);
        break;
      case Op.POP:
        if (stack.length < 1) {
          violations.push({ kind: "underflow", pc, detail: "POP needs 1" });
        } else {
          pushState(fallthrough, stack.slice(0, -1));
        }
        break;
      case Op.DUP:
        if (stack.length < 1) {
          violations.push({ kind: "underflow", pc, detail: "DUP needs 1" });
        } else {
          pushState(fallthrough, [...stack, stack[stack.length - 1]]);
        }
        break;
      case Op.ADD:
        if (stack.length < 2) {
          violations.push({ kind: "underflow", pc, detail: "ADD needs 2" });
        } else {
          const b = stack[stack.length - 1];
          const a = stack[stack.length - 2];
          pushState(fallthrough, [...stack.slice(0, -2), a + b]);
        }
        break;
      case Op.JMP:
        pushState(instr.arg!, stack);
        break;
      case Op.JZ:
        if (stack.length < 1) {
          violations.push({ kind: "underflow", pc, detail: "JZ needs 1" });
        } else {
          const v = stack[stack.length - 1];
          const rest = stack.slice(0, -1);
          pushState(v === 0 ? instr.arg! : fallthrough, rest);
        }
        break;
      case Op.RET:
        if (stack.length !== 1) {
          violations.push({ kind: "ret-height", pc, detail: `height ${stack.length}` });
        }
        break;
    }
  }

  return { violations, truncated, statesExplored: visited.size };
}

// ---------------------------------------------------------------------------
// Program generators
// ---------------------------------------------------------------------------

function encodeSeq(seq: number[][]): Uint8Array {
  return Uint8Array.from(seq.flat());
}

// All instruction sequences of length 1..maxInstrs over a small alphabet,
// with jump targets in 0..maxTarget (deliberately including non-boundaries).
export function* enumeratePrograms(maxInstrs: number, maxTarget: number): Generator<Uint8Array> {
  const templates: number[][] = [
    [Op.PUSH, 0],
    [Op.PUSH, 1],
    [Op.POP],
    [Op.DUP],
    [Op.ADD],
    [Op.RET],
  ];
  for (let t = 0; t <= maxTarget; t++) {
    templates.push([Op.JMP, t], [Op.JZ, t]);
  }
  const seq: number[][] = [];
  function* rec(depth: number): Generator<Uint8Array> {
    if (depth > 0) yield encodeSeq(seq);
    if (depth === maxInstrs) return;
    for (const t of templates) {
      seq.push(t);
      yield* rec(depth + 1);
      seq.pop();
    }
  }
  yield* rec(0);
}

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function* randomPrograms(seed: number, count: number, maxInstrs: number): Generator<Uint8Array> {
  const rand = mulberry32(seed);
  for (let i = 0; i < count; i++) {
    const n = 1 + Math.floor(rand() * maxInstrs);
    const bytes: number[] = [];
    for (let j = 0; j < n; j++) {
      const r = rand();
      if (r < 0.3) bytes.push(Op.PUSH, Math.floor(rand() * 3));
      else if (r < 0.45) bytes.push(Op.POP);
      else if (r < 0.55) bytes.push(Op.DUP);
      else if (r < 0.65) bytes.push(Op.ADD);
      else if (r < 0.8) bytes.push(Op.JMP, Math.floor(rand() * (2 * n + 2)));
      else if (r < 0.95) bytes.push(Op.JZ, Math.floor(rand() * (2 * n + 2)));
      else bytes.push(Op.RET);
    }
    yield Uint8Array.from(bytes);
  }
}

// ---------------------------------------------------------------------------
// Differential cross-check between verifier and concrete exploration
// ---------------------------------------------------------------------------

const STRUCTURAL_KINDS = new Set(["decode", "bad-target"]);

function checkEvidence(code: Uint8Array, err: VerifyError): string | null {
  switch (err.kind) {
    case "decode":
    case "bad-target":
      return null;
    case "underflow": {
      const steps = replayPath(code, err.path);
      const last = steps[steps.length - 1];
      if (!last.underflow) return `underflow evidence at ${err.offset} does not reproduce`;
      if (last.instr.offset !== err.offset) return `underflow evidence ends at wrong offset`;
      if (last.heightBefore !== err.have) return `underflow evidence height mismatch`;
      return null;
    }
    case "ret-height": {
      const steps = replayPath(code, err.path);
      const last = steps[steps.length - 1];
      if (last.instr.op !== Op.RET) return `ret-height evidence does not end at RET`;
      if (last.heightBefore !== err.height) return `ret-height evidence height mismatch`;
      return null;
    }
    case "height-conflict": {
      const a = replayPath(code, err.pathA);
      const b = replayPath(code, err.pathB);
      const lastA = a[a.length - 1];
      const lastB = b[b.length - 1];
      if (lastA.instr.offset !== err.offset || lastB.instr.offset !== err.offset) {
        return `conflict evidence paths do not both end at ${err.offset}`;
      }
      if (lastA.heightBefore !== err.heightA || lastB.heightBefore !== err.heightB) {
        return `conflict evidence heights do not match (${lastA.heightBefore}, ${lastB.heightBefore})`;
      }
      if (err.heightA === err.heightB) return `conflict reported with equal heights`;
      return null;
    }
  }
}

// Returns a failure description, or null if verifier and explorer agree.
export function checkProgram(code: Uint8Array): { failure: string | null; truncated: boolean } {
  const result = verify(code);

  if (!result.ok) {
    for (const err of result.errors) {
      const problem = checkEvidence(code, err);
      if (problem) return { failure: `invalid evidence: ${problem}`, truncated: false };
    }
    // Structural errors need no exploration; path errors are validated by
    // evidence replay above. The verifier may reject value-infeasible paths
    // (over-approximation), so we do NOT require a concrete violation here.
    if (result.errors.every((e) => STRUCTURAL_KINDS.has(e.kind))) {
      return { failure: null, truncated: false };
    }
    return { failure: null, truncated: false };
  }

  // Verifier accepted: concrete exploration must confirm no reachable violation.
  const concrete = exploreConcrete(code);
  if (concrete.truncated) return { failure: null, truncated: true }; // inconclusive, skip
  if (concrete.violations.length > 0) {
    const v = concrete.violations[0];
    return { failure: `verifier accepted but concrete violation: ${v.kind} at ${v.pc} (${v.detail})`, truncated: false };
  }
  return { failure: null, truncated: false };
}
