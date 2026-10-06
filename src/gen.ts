import { Instr, Operand, Program, BinOp } from './ir';

/** mulberry32 种子随机数发生器，保证差分测试可复现。 */
export function makeRng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface Generated {
  program: Program;
  numRegs: number;
  /** 程序中 input 指令按出现顺序对应的外部输入值 */
  inputs: number[];
}

const INT32_MIN_N = -2147483648;
const INT32_MAX_N = 2147483647;

/** 偏向边界值的常量池：容易触发溢出与除零。 */
const INTERESTING = [
  0, 1, -1, 2, -2, 3, 100,
  INT32_MAX_N, INT32_MIN_N, INT32_MAX_N - 1, INT32_MIN_N + 1,
  65536, -65536, 46341, -46341,
];

interface Block {
  instrs: Instr[];
  /** 块结束时确定已定义的寄存器（若 returned 则无意义） */
  definedOut: Set<number>;
  /** 块是否以 ret 结束（其后代码不可达） */
  returned: boolean;
}

export function genProgram(seed: number): Generated {
  const rng = makeRng(seed);
  const numRegs = 6 + Math.floor(rng() * 4); // 6..9 个寄存器
  const maxDepth = 2 + Math.floor(rng() * 3); // 分支嵌套深度
  let nextId = 0;
  let nextLabel = 0;
  const freshLabel = () => `L${nextLabel++}`;
  const inputs: number[] = [];

  const pick = <T>(arr: T[]): T => arr[Math.floor(rng() * arr.length)]!;
  const randValue = (): number =>
    rng() < 0.55 ? pick(INTERESTING) : Math.floor(rng() * 2001) - 1000;

  const genBlock = (depth: number, definedIn: ReadonlySet<number>): Block => {
    const defined = new Set(definedIn);
    const out: Instr[] = [];

    const pickReg = (): number => pick([...defined]);
    const randOperand = (): Operand => {
      if (defined.size === 0 || rng() < 0.45) return { kind: 'imm', value: randValue() };
      return { kind: 'reg', reg: pickReg() };
    };
    const emitArith = (count: number) => {
      for (let k = 0; k < count; k++) {
        const dst = Math.floor(rng() * numRegs);
        const roll = rng();
        if (defined.size > 0 && roll < 0.15) {
          // input：外部输入，优化器无法得知其值，分支不可折叠
          out.push({ id: nextId++, op: 'input', dst });
          inputs.push(randValue());
        } else if (defined.size === 0 || roll < 0.45) {
          out.push({ id: nextId++, op: 'const', dst, value: randValue() });
        } else {
          // 提高 div/mod 占比，更容易产生除零错误
          const op: BinOp =
            rng() < 0.3
              ? pick(['div', 'mod'] as BinOp[])
              : pick(['add', 'sub', 'mul', 'div', 'mod'] as BinOp[]);
          out.push({ id: nextId++, op, dst, a: randOperand(), b: randOperand() });
        }
        defined.add(dst);
      }
    };

    emitArith(1 + Math.floor(rng() * 4));

    // 小概率提前返回
    if (rng() < 0.12) {
      out.push({ id: nextId++, op: 'ret', src: randOperand() });
      return { instrs: out, definedOut: defined, returned: true };
    }

    if (depth < maxDepth && rng() < 0.65) {
      const thenLabel = freshLabel();
      const elseLabel = freshLabel();
      const mergeLabel = freshLabel();
      const cond: Operand =
        defined.size > 0 && rng() < 0.8
          ? { kind: 'reg', reg: pickReg() }
          : { kind: 'imm', value: randValue() };
      out.push({ id: nextId++, op: 'br', cond, thenLabel, elseLabel });

      const before = new Set(defined);
      const thenBlk = genBlock(depth + 1, before);
      thenBlk.instrs[0]!.label = thenLabel;
      const elseBlk = genBlock(depth + 1, before);
      elseBlk.instrs[0]!.label = elseLabel;

      out.push(...thenBlk.instrs);
      if (!thenBlk.returned) out.push({ id: nextId++, op: 'jmp', target: mergeLabel });
      out.push(...elseBlk.instrs);
      if (!elseBlk.returned) out.push({ id: nextId++, op: 'jmp', target: mergeLabel });

      if (thenBlk.returned && elseBlk.returned) {
        // 汇合点不可达：整个块到此为止
        return { instrs: out, definedOut: new Set(), returned: true };
      }

      // 汇合点确定已定义的寄存器 = 各“能到达汇合点”分支的交集
      const mergeDefined = new Set<number>(
        thenBlk.returned ? elseBlk.definedOut : thenBlk.definedOut,
      );
      const other = elseBlk.returned ? null : elseBlk.definedOut;
      if (!thenBlk.returned && other) {
        for (const r of [...mergeDefined]) if (!other.has(r)) mergeDefined.delete(r);
      }

      // 汇合后继续：下一条指令携带 mergeLabel
      defined.clear();
      mergeDefined.forEach((r) => defined.add(r));
      const contStart = out.length;
      emitArith(1 + Math.floor(rng() * 3));
      out[contStart]!.label = mergeLabel;
      return { instrs: out, definedOut: defined, returned: false };
    }

    return { instrs: out, definedOut: defined, returned: false };
  };

  const main = genBlock(0, new Set());
  const instrs = main.instrs;
  if (!main.returned) {
    const src: Operand =
      main.definedOut.size > 0 && rng() < 0.8
        ? { kind: 'reg', reg: pick([...main.definedOut]) }
        : { kind: 'imm', value: randValue() };
    instrs.push({ id: nextId++, op: 'ret', src });
  }

  const program: Program = { instrs };
  validate(program);
  return { program, numRegs, inputs };
}

/** 生成器自检：标签可解析、寄存器先定义后使用（沿所有路径）。 */
function validate(program: Program): void {
  const labels = new Set<string>();
  for (const ins of program.instrs) {
    if (ins.label !== undefined) {
      if (labels.has(ins.label)) throw new Error(`gen: duplicate label ${ins.label}`);
      labels.add(ins.label);
    }
  }
  const labelToIndex = new Map<string, number>();
  program.instrs.forEach((ins, i) => {
    if (ins.label !== undefined) labelToIndex.set(ins.label, i);
  });
  const n = program.instrs.length;
  const inDefs: Array<Set<number> | null> = new Array(n).fill(null);
  inDefs[0] = new Set();
  const worklist = [0];
  const useRegs = (ins: Instr): number[] => {
    const regs: number[] = [];
    const addOp = (o: Operand) => {
      if (o.kind === 'reg') regs.push(o.reg);
    };
    switch (ins.op) {
      case 'add':
      case 'sub':
      case 'mul':
      case 'div':
      case 'mod':
        addOp(ins.a);
        addOp(ins.b);
        break;
      case 'br':
        addOp(ins.cond);
        break;
      case 'ret':
        addOp(ins.src);
        break;
    }
    return regs;
  };
  while (worklist.length > 0) {
    const i = worklist.pop()!;
    const ins = program.instrs[i]!;
    const defs = inDefs[i]!;
    for (const r of useRegs(ins)) {
      if (!defs.has(r)) throw new Error(`gen: use of undefined r${r} at #${ins.id}`);
    }
    const outDefs = new Set(defs);
    if (ins.op === 'const' || ins.op === 'input' || ins.op === 'add' || ins.op === 'sub' ||
        ins.op === 'mul' || ins.op === 'div' || ins.op === 'mod') {
      outDefs.add(ins.dst);
    }
    const resolve = (label: string): number => {
      const idx = labelToIndex.get(label);
      if (idx === undefined) throw new Error(`gen: unresolved label ${label}`);
      return idx;
    };
    const succs: number[] = [];
    if (ins.op === 'br') {
      succs.push(resolve(ins.thenLabel), resolve(ins.elseLabel));
    } else if (ins.op === 'jmp') {
      succs.push(resolve(ins.target));
    } else if (ins.op !== 'ret' && i + 1 < n) {
      succs.push(i + 1);
    }
    for (const s of succs) {
      const prev = inDefs[s]!;
      if (prev === null) {
        inDefs[s] = outDefs;
        worklist.push(s);
      } else {
        const inter = new Set([...prev].filter((r) => outDefs.has(r)));
        if (inter.size !== prev.size) {
          inDefs[s] = inter;
          worklist.push(s);
        }
      }
    }
  }
}
