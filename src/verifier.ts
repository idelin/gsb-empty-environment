import { Op, decode, disassemble, type Instr } from "./isa.ts";

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export type VerifyError =
  | { kind: "decode"; offset: number; message: string }
  | { kind: "bad-target"; offset: number; target: number }
  | { kind: "underflow"; offset: number; need: number; have: number; path: number[] }
  | { kind: "ret-height"; offset: number; height: number; path: number[] }
  | {
      kind: "height-conflict";
      offset: number;
      heightA: number;
      heightB: number;
      pathA: number[];
      pathB: number[];
    };

export type VerifyResult =
  | { ok: true; heights: Map<number, number> }
  | { ok: false; errors: VerifyError[] };

// ---------------------------------------------------------------------------
// Stack effects
// ---------------------------------------------------------------------------

function effect(instr: Instr): { need: number; delta: number } {
  switch (instr.op) {
    case Op.PUSH:
      return { need: 0, delta: +1 };
    case Op.POP:
      return { need: 1, delta: -1 };
    case Op.DUP:
      return { need: 1, delta: +1 };
    case Op.ADD:
      return { need: 2, delta: -1 };
    case Op.JMP:
      return { need: 0, delta: 0 };
    case Op.JZ:
      return { need: 1, delta: -1 };
    case Op.RET:
      return { need: 0, delta: 0 };
  }
}

// ---------------------------------------------------------------------------
// Verifier: abstract interpretation over stack heights.
//
// Abstract state per instruction offset = exact stack height (a number).
// Entry height at offset 0 is 0. Merging two different heights at the same
// offset is a height-conflict error; the two witness paths are reported.
// Unreachable instructions never receive a state and are never checked,
// so dead "bad" code cannot cause false positives.
// ---------------------------------------------------------------------------

export function verify(code: Uint8Array): VerifyResult {
  const decoded = decode(code);
  if (!decoded.ok) {
    return { ok: false, errors: [{ kind: "decode", offset: decoded.offset, message: decoded.error }] };
  }
  const { instrs, byOffset } = decoded;

  // Structural check (whole program): jump targets must be instruction boundaries.
  const structural: VerifyError[] = [];
  for (const instr of instrs) {
    if (instr.op === Op.JMP || instr.op === Op.JZ) {
      const target = instr.arg!;
      if (!byOffset.has(target)) {
        structural.push({ kind: "bad-target", offset: instr.offset, target });
      }
    }
  }
  if (structural.length > 0) return { ok: false, errors: structural };

  interface State {
    height: number;
    path: number[]; // offsets visited, including this one
  }

  const errors: VerifyError[] = [];
  const states = new Map<number, State>();
  const queue: Array<{ offset: number; height: number; path: number[] }> = [];

  const enqueue = (offset: number, height: number, path: number[]): void => {
    const prev = states.get(offset);
    if (prev) {
      if (prev.height !== height) {
        errors.push({
          kind: "height-conflict",
          offset,
          heightA: prev.height,
          heightB: height,
          pathA: prev.path,
          pathB: path,
        });
      }
      return; // first height wins; conflicting heights are errors, never merged
    }
    states.set(offset, { height, path });
    queue.push({ offset, height, path });
  };

  if (byOffset.has(0)) enqueue(0, 0, [0]);

  while (queue.length > 0) {
    const { offset, height, path } = queue.shift()!;
    const instr = byOffset.get(offset);
    if (!instr) continue;
    const eff = effect(instr);

    if (height < eff.need) {
      errors.push({ kind: "underflow", offset, need: eff.need, have: height, path });
      continue; // no meaningful successor state
    }
    const nextHeight = height + eff.delta;
    const fallthrough = offset + instr.size;

    switch (instr.op) {
      case Op.RET:
        if (height !== 1) {
          errors.push({ kind: "ret-height", offset, height, path });
        }
        break; // no successors
      case Op.JMP:
        enqueue(instr.arg!, nextHeight, [...path, instr.arg!]);
        break;
      case Op.JZ:
        enqueue(instr.arg!, nextHeight, [...path, instr.arg!]);
        if (byOffset.has(fallthrough)) enqueue(fallthrough, nextHeight, [...path, fallthrough]);
        break;
      default:
        // Falling off the last instruction simply ends the path.
        if (byOffset.has(fallthrough)) enqueue(fallthrough, nextHeight, [...path, fallthrough]);
    }
  }

  if (errors.length > 0) return { ok: false, errors };
  const heights = new Map<number, number>();
  for (const [off, st] of states) heights.set(off, st.height);
  return { ok: true, heights };
}

// ---------------------------------------------------------------------------
// Path replay: validates a witness path structurally (each step goes to a
// possible successor) and tracks the stack height via stack effects.
// Deliberately value-insensitive, matching the verifier's abstraction.
// ---------------------------------------------------------------------------

export interface ReplayStep {
  instr: Instr;
  heightBefore: number;
  heightAfter: number;
  underflow: boolean;
}

// Offsets this instruction may transfer control to (fallthrough included
// even if it runs off the end; callers check membership in byOffset).
export function structuralSuccessors(instr: Instr): number[] {
  switch (instr.op) {
    case Op.JMP:
      return [instr.arg!];
    case Op.JZ:
      return [instr.arg!, instr.offset + instr.size];
    case Op.RET:
      return [];
    default:
      return [instr.offset + instr.size];
  }
}

export function replayPath(code: Uint8Array, path: number[]): ReplayStep[] {
  const decoded = decode(code);
  if (!decoded.ok) throw new Error(`cannot replay: ${decoded.error}`);
  const steps: ReplayStep[] = [];
  let height = 0;
  for (let i = 0; i < path.length; i++) {
    const instr = decoded.byOffset.get(path[i]);
    if (!instr) throw new Error(`path offset ${path[i]} is not an instruction boundary`);
    const eff = effect(instr);
    const underflow = height < eff.need;
    const heightAfter = underflow ? height : height + eff.delta;
    steps.push({ instr, heightBefore: height, heightAfter, underflow });
    if (underflow) break;
    height = heightAfter;
    if (i + 1 < path.length && !structuralSuccessors(instr).includes(path[i + 1])) {
      throw new Error(`broken path: offset ${path[i + 1]} is not a successor of ${instr.offset}`);
    }
  }
  return steps;
}

// ---------------------------------------------------------------------------
// Evidence formatting
// ---------------------------------------------------------------------------

function renderPath(code: Uint8Array, path: number[], indent: string): string[] {
  const lines: string[] = [];
  const steps = replayPath(code, path);
  for (let i = 0; i < steps.length; i++) {
    const s = steps[i];
    const marker = i === steps.length - 1 ? "=>" : "  ";
    const text = `${s.instr.offset}: ${disassemble(s.instr)}`.padEnd(16);
    const note = s.underflow ? "  <-- UNDERFLOW" : "";
    lines.push(`${indent}${marker} ${text}; height ${s.heightBefore} -> ${s.heightAfter}${note}`);
  }
  return lines;
}

export function formatError(code: Uint8Array, err: VerifyError): string {
  switch (err.kind) {
    case "decode":
      return `decode error at byte ${err.offset}: ${err.message}`;
    case "bad-target":
      return `jump at ${err.offset} targets byte ${err.target}, which is not an instruction boundary`;
    case "underflow":
      return [
        `stack underflow at ${err.offset}: needs ${err.need}, has ${err.have}`,
        ...renderPath(code, err.path, "  "),
      ].join("\n");
    case "ret-height":
      return [
        `RET at ${err.offset} with stack height ${err.height}, expected exactly 1`,
        ...renderPath(code, err.path, "  "),
      ].join("\n");
    case "height-conflict":
      return [
        `conflicting entry stack heights at ${err.offset}: ${err.heightA} vs ${err.heightB}`,
        `  path A (height ${err.heightA}):`,
        ...renderPath(code, err.pathA, "    "),
        `  path B (height ${err.heightB}):`,
        ...renderPath(code, err.pathB, "    "),
      ].join("\n");
  }
}

export function formatErrors(code: Uint8Array, errors: VerifyError[]): string {
  return errors.map((e) => formatError(code, e)).join("\n");
}
