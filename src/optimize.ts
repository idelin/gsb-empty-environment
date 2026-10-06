import { BinOp, Instr, Operand, Program, EvalError, evalBinOp } from './ir';

/**
 * 常量传播 / 常量折叠优化器。
 *
 * 安全性约束：
 * - 溢出与除零是可见错误：常量折叠只在“求值确定不触发错误”时进行；
 *   确定会触发错误的指令（如常量 1/0）原样保留，错误仍在同一指令 id 上抛出。
 * - 不做代数恒等式化简（如 x*0 -> 0）：x 的求值可能出错，不能因化简而消除。
 * - 只删除“可证明不可达”的指令（数据流分析中入口状态为 bot 的指令），
 *   即确定不会被任何执行路径到达的分支；可到达指令一律保留。
 * - 所有保留/重写的指令保持原 id，保证错误位置与执行轨迹可比较。
 */

type Lattice = { kind: 'const'; v: bigint } | { kind: 'unknown' };
/** bot 表示该程序点不可达 */
type State = Lattice[] | 'bot';

const UNKNOWN: Lattice = { kind: 'unknown' };

function joinState(a: State, b: State, nregs: number): State {
  if (a === 'bot') return b;
  if (b === 'bot') return a;
  const out: Lattice[] = new Array(nregs);
  for (let r = 0; r < nregs; r++) {
    const x = a[r]!;
    const y = b[r]!;
    out[r] = x.kind === 'const' && y.kind === 'const' && x.v === y.v ? x : UNKNOWN;
  }
  return out;
}

function stateEqual(a: State, b: State, nregs: number): boolean {
  if (a === 'bot' || b === 'bot') return a === b;
  for (let r = 0; r < nregs; r++) {
    const x = a[r]!;
    const y = b[r]!;
    if (x.kind !== y.kind) return false;
    if (x.kind === 'const' && y.kind === 'const' && x.v !== y.v) return false;
  }
  return true;
}

export interface OptimizeResult {
  program: Program;
  removed: number;
  folded: number;
  branchesFolded: number;
}

export function optimize(program: Program, numRegs: number): OptimizeResult {
  const instrs = program.instrs;
  const n = instrs.length;
  const labelToIndex = new Map<string, number>();
  instrs.forEach((ins, i) => {
    if (ins.label !== undefined) labelToIndex.set(ins.label, i);
  });
  const targetOf = (label: string): number => {
    const idx = labelToIndex.get(label);
    if (idx === undefined) throw new Error(`internal: unknown label ${label}`);
    return idx;
  };

  // ---- 前向数据流分析（worklist）----
  const inState: State[] = new Array<State>(n).fill('bot');
  const entry: State = new Array<Lattice>(numRegs).fill(UNKNOWN);
  inState[0] = entry;
  const worklist: number[] = [0];

  const operandLattice = (st: State, o: Operand): Lattice => {
    if (o.kind === 'imm') return { kind: 'const', v: BigInt(o.value) };
    if (st === 'bot') return UNKNOWN;
    return st[o.reg] ?? UNKNOWN;
  };

  /** 返回指令的后继及对应出口状态；确定出错的指令没有后继。 */
  const transferAt = (i: number, st: State): Array<[number, State]> => {
    if (st === 'bot') return [];
    const ins = instrs[i]!;
    switch (ins.op) {
      case 'const': {
        const out = st.slice();
        out[ins.dst] = { kind: 'const', v: BigInt(ins.value) };
        return i + 1 < n ? [[i + 1, out]] : [];
      }
      case 'input': {
        const out = st.slice();
        out[ins.dst] = UNKNOWN;
        return i + 1 < n ? [[i + 1, out]] : [];
      }
      case 'add':
      case 'sub':
      case 'mul':
      case 'div':
      case 'mod': {
        const la = operandLattice(st, ins.a);
        const lb = operandLattice(st, ins.b);
        if (la.kind === 'const' && lb.kind === 'const') {
          try {
            const v = evalBinOp(ins.op as BinOp, la.v, lb.v);
            const out = st.slice();
            out[ins.dst] = { kind: 'const', v };
            return i + 1 < n ? [[i + 1, out]] : [];
          } catch (e) {
            if (e instanceof EvalError) return []; // 确定出错：执行在此停止
            throw e;
          }
        }
        const out = st.slice();
        out[ins.dst] = UNKNOWN;
        return i + 1 < n ? [[i + 1, out]] : [];
      }
      case 'br': {
        const lc = operandLattice(st, ins.cond);
        if (lc.kind === 'const') {
          const t = lc.v !== 0n ? targetOf(ins.thenLabel) : targetOf(ins.elseLabel);
          return [[t, st]];
        }
        return [
          [targetOf(ins.thenLabel), st],
          [targetOf(ins.elseLabel), st],
        ];
      }
      case 'jmp':
        return [[targetOf(ins.target), st]];
      case 'ret':
        return [];
    }
  };

  while (worklist.length > 0) {
    const i = worklist.pop()!;
    const st = inState[i]!;
    for (const [succ, out] of transferAt(i, st)) {
      const merged = joinState(inState[succ]!, out, numRegs);
      if (!stateEqual(merged, inState[succ]!, numRegs)) {
        inState[succ] = merged;
        worklist.push(succ);
      }
    }
  }

  // ---- 重写 ----
  const outInstrs: Instr[] = [];
  let removed = 0;
  let folded = 0;
  let branchesFolded = 0;

  const constOperand = (st: State, o: Operand): Operand => {
    if (o.kind === 'imm' || st === 'bot') return o;
    const l = st[o.reg];
    return l && l.kind === 'const' ? { kind: 'imm', value: Number(l.v) } : o;
  };

  for (let i = 0; i < n; i++) {
    const ins = instrs[i]!;
    const st = inState[i]!;
    if (st === 'bot') {
      removed++; // 可证明不可达，删除
      continue;
    }
    switch (ins.op) {
      case 'add':
      case 'sub':
      case 'mul':
      case 'div':
      case 'mod': {
        const la = operandLattice(st, ins.a);
        const lb = operandLattice(st, ins.b);
        if (la.kind === 'const' && lb.kind === 'const') {
          let traps = false;
          try {
            evalBinOp(ins.op, la.v, lb.v);
          } catch (e) {
            if (e instanceof EvalError) traps = true;
            else throw e;
          }
          if (!traps) {
            const v = evalBinOp(ins.op, la.v, lb.v);
            outInstrs.push({ id: ins.id, op: 'const', dst: ins.dst, value: Number(v), label: ins.label });
            folded++;
            continue;
          }
          // 确定出错：原样保留，错误位置不变
          outInstrs.push(ins);
          continue;
        }
        outInstrs.push({ ...ins, a: constOperand(st, ins.a), b: constOperand(st, ins.b) });
        continue;
      }
      case 'br': {
        const lc = operandLattice(st, ins.cond);
        if (lc.kind === 'const') {
          const target = lc.v !== 0n ? ins.thenLabel : ins.elseLabel;
          outInstrs.push({ id: ins.id, op: 'jmp', target, label: ins.label });
          branchesFolded++;
          continue;
        }
        outInstrs.push({ ...ins, cond: constOperand(st, ins.cond) });
        continue;
      }
      case 'ret':
        outInstrs.push({ ...ins, src: constOperand(st, ins.src) });
        continue;
      default:
        outInstrs.push(ins);
    }
  }

  return { program: { instrs: outInstrs }, removed, folded, branchesFolded };
}
