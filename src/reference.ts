import { Op, decode } from "./isa";

export interface RefResult {
  ok: boolean;
  reason?: string;
}

export function checkReference(code: Uint8Array, maxStack = 32): RefResult {
  const dec = decode(code);
  if (!dec.ok) {
    return { ok: false, reason: `decode: ${dec.message}` };
  }
  if (dec.instrs.length === 0) {
    return { ok: false, reason: "empty program" };
  }

  const heightsAt = new Map<number, Set<number>>();
  let failure: string | undefined;

  const visit = (offset: number, height: number, onPath: Set<string>): void => {
    if (failure) {
      return;
    }
    const key = `${offset}:${height}`;
    if (onPath.has(key)) {
      return;
    }
    onPath.add(key);

    let set = heightsAt.get(offset);
    if (!set) {
      set = new Set<number>();
      heightsAt.set(offset, set);
    }
    set.add(height);

    const instr = dec.byOffset.get(offset)!;
    const next = offset + instr.size;
    switch (instr.op) {
      case Op.Push:
        if (height + 1 > maxStack) {
          failure = `stack overflow at ${offset}`;
        } else if (next >= code.length) {
          failure = `falls off end at ${offset}`;
        } else {
          visit(next, height + 1, onPath);
        }
        break;
      case Op.Pop:
        if (height < 1) {
          failure = `underflow at ${offset}`;
        } else if (next >= code.length) {
          failure = `falls off end at ${offset}`;
        } else {
          visit(next, height - 1, onPath);
        }
        break;
      case Op.Add:
        if (height < 2) {
          failure = `underflow at ${offset}`;
        } else if (next >= code.length) {
          failure = `falls off end at ${offset}`;
        } else {
          visit(next, height - 1, onPath);
        }
        break;
      case Op.Jmp:
        if (!dec.boundaries.has(instr.target!)) {
          failure = `bad jump target ${instr.target} at ${offset}`;
        } else {
          visit(instr.target!, height, onPath);
        }
        break;
      case Op.Jz:
        if (height < 1) {
          failure = `underflow at ${offset}`;
        } else if (!dec.boundaries.has(instr.target!)) {
          failure = `bad jump target ${instr.target} at ${offset}`;
        } else {
          visit(instr.target!, height - 1, onPath);
          if (next >= code.length) {
            failure = `falls off end at ${offset}`;
          } else {
            visit(next, height - 1, onPath);
          }
        }
        break;
      case Op.Ret:
        if (height !== 1) {
          failure = `return height ${height} at ${offset}`;
        }
        break;
    }
    onPath.delete(key);
  };

  visit(0, 0, new Set<string>());
  if (failure) {
    return { ok: false, reason: failure };
  }
  for (const [offset, set] of heightsAt) {
    if (set.size > 1) {
      return { ok: false, reason: `height conflict at ${offset}: ${[...set].join(",")}` };
    }
  }
  return { ok: true };
}
