import { Op, decode, disasm } from "./isa";

export interface Step {
  offset: number;
  height: number;
}

export type VerifyError =
  | { kind: "decode"; at: number; message: string }
  | { kind: "empty-program" }
  | { kind: "underflow"; offset: number; height: number; needed: number; path: Step[] }
  | { kind: "stack-overflow"; offset: number; height: number; max: number; path: Step[] }
  | { kind: "bad-return-height"; offset: number; height: number; path: Step[] }
  | { kind: "bad-jump-target"; offset: number; target: number; path: Step[] }
  | { kind: "falls-off-end"; offset: number; path: Step[] }
  | {
      kind: "height-conflict";
      offset: number;
      heightA: number;
      pathA: Step[];
      heightB: number;
      pathB: Step[];
    };

export type VerifyResult = { ok: true } | { ok: false; error: VerifyError };

export interface Analysis {
  result: VerifyResult;
  reachable: Map<number, number>;
}

export function analyze(code: Uint8Array, maxStack = 32): Analysis {
  const reachable = new Map<number, number>();
  const dec = decode(code);
  if (!dec.ok) {
    return { result: { ok: false, error: { kind: "decode", at: dec.at, message: dec.message } }, reachable };
  }
  if (dec.instrs.length === 0) {
    return { result: { ok: false, error: { kind: "empty-program" } }, reachable };
  }

  const pred = new Map<number, number>();
  const fail = (error: VerifyError): Analysis => ({ result: { ok: false, error }, reachable });

  const buildPath = (offset: number): Step[] => {
    const rev: Step[] = [];
    let cur: number | undefined = offset;
    while (cur !== undefined) {
      rev.push({ offset: cur, height: reachable.get(cur)! });
      cur = pred.get(cur);
    }
    return rev.reverse();
  };

  const worklist: number[] = [0];
  reachable.set(0, 0);

  while (worklist.length > 0) {
    const offset = worklist.pop()!;
    const height = reachable.get(offset)!;
    const instr = dec.byOffset.get(offset)!;

    const propagate = (next: number, nextHeight: number): VerifyError | undefined => {
      if (nextHeight > maxStack) {
        return { kind: "stack-overflow", offset, height: nextHeight, max: maxStack, path: buildPath(offset) };
      }
      const existing = reachable.get(next);
      if (existing === undefined) {
        reachable.set(next, nextHeight);
        pred.set(next, offset);
        worklist.push(next);
        return undefined;
      }
      if (existing !== nextHeight) {
        return {
          kind: "height-conflict",
          offset: next,
          heightA: existing,
          pathA: buildPath(next),
          heightB: nextHeight,
          pathB: buildPath(offset).concat({ offset: next, height: nextHeight }),
        };
      }
      return undefined;
    };

    const succs: Array<{ to: number; height: number }> = [];
    switch (instr.op) {
      case Op.Push:
        succs.push({ to: offset + 1, height: height + 1 });
        break;
      case Op.Pop:
        if (height < 1) {
          return fail({ kind: "underflow", offset, height, needed: 1, path: buildPath(offset) });
        }
        succs.push({ to: offset + 1, height: height - 1 });
        break;
      case Op.Add:
        if (height < 2) {
          return fail({ kind: "underflow", offset, height, needed: 2, path: buildPath(offset) });
        }
        succs.push({ to: offset + 1, height: height - 1 });
        break;
      case Op.Jmp:
        if (!dec.boundaries.has(instr.target!)) {
          return fail({ kind: "bad-jump-target", offset, target: instr.target!, path: buildPath(offset) });
        }
        succs.push({ to: instr.target!, height });
        break;
      case Op.Jz:
        if (height < 1) {
          return fail({ kind: "underflow", offset, height, needed: 1, path: buildPath(offset) });
        }
        if (!dec.boundaries.has(instr.target!)) {
          return fail({ kind: "bad-jump-target", offset, target: instr.target!, path: buildPath(offset) });
        }
        succs.push({ to: instr.target!, height: height - 1 });
        succs.push({ to: offset + 3, height: height - 1 });
        break;
      case Op.Ret:
        if (height !== 1) {
          return fail({ kind: "bad-return-height", offset, height, path: buildPath(offset) });
        }
        break;
    }

    for (const succ of succs) {
      if (succ.to >= code.length) {
        return fail({ kind: "falls-off-end", offset, path: buildPath(offset) });
      }
      const err = propagate(succ.to, succ.height);
      if (err) {
        return fail(err);
      }
    }
  }

  return { result: { ok: true }, reachable };
}

export function verify(code: Uint8Array, maxStack = 32): VerifyResult {
  return analyze(code, maxStack).result;
}

export function formatEvidence(code: Uint8Array, error: VerifyError): string {
  const dec = decode(code);
  const name = (offset: number): string => {
    if (dec.ok) {
      const instr = dec.byOffset.get(offset);
      if (instr) {
        return disasm(instr);
      }
    }
    return `<offset ${offset}>`;
  };
  const fmtPath = (path: Step[]): string =>
    path
      .map((s) => `    ${String(s.offset).padStart(4)}  ${name(s.offset).padEnd(10)} entry-height=${s.height}`)
      .join("\n");

  switch (error.kind) {
    case "decode":
      return `decode error at byte ${error.at}: ${error.message}`;
    case "empty-program":
      return "empty program";
    case "underflow":
      return (
        `stack underflow at offset ${error.offset} (${name(error.offset)}): ` +
        `height ${error.height}, needs ${error.needed}\n  path:\n${fmtPath(error.path)}`
      );
    case "stack-overflow":
      return (
        `stack overflow at offset ${error.offset} (${name(error.offset)}): ` +
        `resulting height ${error.height} exceeds max ${error.max}\n  path:\n${fmtPath(error.path)}`
      );
    case "bad-return-height":
      return (
        `return with stack height ${error.height} (expected 1) at offset ${error.offset} ` +
        `(${name(error.offset)})\n  path:\n${fmtPath(error.path)}`
      );
    case "bad-jump-target":
      return (
        `jump to non-boundary offset ${error.target} from offset ${error.offset} ` +
        `(${name(error.offset)})\n  path:\n${fmtPath(error.path)}`
      );
    case "falls-off-end":
      return `control falls off the end after offset ${error.offset} (${name(error.offset)})\n  path:\n${fmtPath(error.path)}`;
    case "height-conflict":
      return (
        `height conflict at offset ${error.offset} (${name(error.offset)}): ` +
        `${error.heightA} vs ${error.heightB}\n` +
        `  path A (entry height ${error.heightA}):\n${fmtPath(error.pathA)}\n` +
        `  path B (entry height ${error.heightB}):\n${fmtPath(error.pathB)}`
      );
  }
}
