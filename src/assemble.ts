/**
 * Streaming record-stream -> JS tree assembly. Fully iterative: the open
 * containers live on an explicit stack, so depth is limited by heap, not by
 * the call stack. Structural validation is O(1) per record.
 */
import { BuildError } from './errors.ts';
import { Op, type Rec } from './format.ts';
import { pathDepth, type Path } from './path.ts';

function scalarOf(rec: Rec): unknown {
  switch (rec.op) {
    case Op.Null:
      return null;
    case Op.False:
      return false;
    case Op.True:
      return true;
    case Op.Num:
    case Op.Str:
      return rec.val;
    default:
      throw new BuildError(`op ${rec.op} is not a scalar`, rec.path);
  }
}

export class Assembler {
  private stack: Array<Record<string, unknown> | unknown[]> = [];
  private rootDone = false;
  private result: unknown;

  push(rec: Rec): void {
    const d = pathDepth(rec.path);
    if (rec.op === Op.Missing) {
      throw new BuildError('tombstone (missing) record is only valid inside a patch', rec.path);
    }
    if (d === 0 && this.rootDone) throw new BuildError('multiple root definitions', rec.path);
    while (this.stack.length > d) this.stack.pop();
    if (d > 0) {
      if (this.stack.length < d) {
        throw new BuildError('missing parent definition (broken reference)', rec.path!.up);
      }
      const parent = this.stack[d - 1];
      const seg = rec.path!.seg;
      if (Array.isArray(parent)) {
        if (seg.t !== 'i') {
          throw new BuildError(`object key ${JSON.stringify(seg.k)} appears inside an array`, rec.path);
        }
      } else if (seg.t !== 'k') {
        throw new BuildError(`array index [${seg.i}] appears inside an object`, rec.path);
      }
    }
    switch (rec.op) {
      case Op.Obj:
      case Op.Arr: {
        const node: Record<string, unknown> | unknown[] = rec.op === Op.Obj ? {} : [];
        this.attach(rec.path, d, node);
        this.stack.push(node);
        break;
      }
      case Op.Hole: {
        if (d === 0 || rec.path!.seg.t !== 'i') {
          throw new BuildError('array hole outside an array', rec.path);
        }
        break; // leave the slot empty; array length comes from later siblings
      }
      default:
        this.attach(rec.path, d, scalarOf(rec));
    }
  }

  private attach(path: Path, d: number, val: unknown): void {
    if (d === 0) {
      this.result = val;
      this.rootDone = true;
      return;
    }
    const parent = this.stack[d - 1] as Record<string, unknown> & unknown[];
    const seg = path!.seg;
    if (seg.t === 'i') parent[seg.i] = val;
    else parent[seg.k] = val;
  }

  finish(): unknown {
    if (!this.rootDone) throw new BuildError('empty record stream: no root record', null);
    return this.result;
  }
}

export function assemble(recs: Iterable<Rec>): unknown {
  const asm = new Assembler();
  for (const rec of recs) asm.push(rec);
  return asm.finish();
}

/** Node-by-node structural equality, iterative (safe at any depth). */
export function iterativeEqual(a: unknown, b: unknown): boolean {
  const stack: Array<[unknown, unknown]> = [[a, b]];
  while (stack.length > 0) {
    const [x, y] = stack.pop()!;
    if (Object.is(x, y)) continue;
    if (typeof x !== 'object' || typeof y !== 'object' || x === null || y === null) return false;
    const xa = Array.isArray(x);
    if (xa !== Array.isArray(y)) return false;
    if (xa) {
      const xs = x as unknown[];
      const ys = y as unknown[];
      if (xs.length !== ys.length) return false;
      for (let i = 0; i < xs.length; i++) {
        if (i in xs !== i in ys) return false; // holes must match
        if (i in xs) stack.push([xs[i], ys[i]]);
      }
    } else {
      const kx = Object.keys(x as object).sort();
      const ky = Object.keys(y as object).sort();
      if (kx.length !== ky.length) return false;
      for (let i = 0; i < kx.length; i++) {
        if (kx[i] !== ky[i]) return false;
        stack.push([(x as Record<string, unknown>)[kx[i]], (y as Record<string, unknown>)[kx[i]]]);
      }
    }
  }
  return true;
}
