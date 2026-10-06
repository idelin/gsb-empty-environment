import { Program } from './ir';
import { run, RunResult } from './interp';
import { optimize } from './optimize';
import { formatProgram } from './ir';

/** 手写定向用例：覆盖用户关心的安全性约束。 */

function key(r: RunResult): string {
  const o = r.outcome;
  const out = o.ok ? `ok:${o.value}` : `err:${o.error}@#${o.instrId}`;
  return `${out} trace=[${r.trace.join(',')}]`;
}

let failures = 0;

function check(name: string, program: Program, numRegs: number, expect: string, inputs: number[] = []): void {
  const original = run(program, inputs);
  const opt = optimize(program, numRegs);
  const optimized = run(opt.program, inputs);
  const gotOrig = key(original);
  const gotOpt = key(optimized);
  const ok = gotOrig === expect && gotOpt === expect;
  if (!ok) {
    failures++;
    console.error(`FAIL: ${name}`);
    console.error(`  expect    ${expect}`);
    console.error(`  original  ${gotOrig}`);
    console.error(`  optimized ${gotOpt}`);
    console.error('  --- optimized program ---');
    console.error('  ' + formatProgram(opt.program).split('\n').join('\n  '));
  } else {
    console.log(`pass: ${name}`);
  }
}

// 1. x*0 不能消除 x 的求值异常：x = 1/0 出错，错误必须仍在 #1 抛出
check(
  'x*0 keeps x evaluation error',
  {
    instrs: [
      { id: 1, op: 'div', dst: 0, a: { kind: 'imm', value: 1 }, b: { kind: 'imm', value: 0 } },
      { id: 2, op: 'mul', dst: 1, a: { kind: 'reg', reg: 0 }, b: { kind: 'imm', value: 0 } },
      { id: 3, op: 'ret', src: { kind: 'reg', reg: 1 } },
    ],
  },
  2,
  'err:div-by-zero@#1 trace=[1]',
);

// 2. 溢出是可见错误：INT32_MAX+1 不能折叠成普通常量，错误位置保持 #5
check(
  'overflowing add is not folded away',
  {
    instrs: [
      { id: 5, op: 'add', dst: 0, a: { kind: 'imm', value: 2147483647 }, b: { kind: 'imm', value: 1 } },
      { id: 6, op: 'ret', src: { kind: 'reg', reg: 0 } },
    ],
  },
  1,
  'err:overflow@#5 trace=[5]',
);

// 3. INT32_MIN / -1 溢出；INT32_MIN % -1 = 0 不溢出
check(
  'INT_MIN / -1 overflows',
  {
    instrs: [
      { id: 1, op: 'div', dst: 0, a: { kind: 'imm', value: -2147483648 }, b: { kind: 'imm', value: -1 } },
      { id: 2, op: 'ret', src: { kind: 'reg', reg: 0 } },
    ],
  },
  1,
  'err:overflow@#1 trace=[1]',
);
check(
  'INT_MIN % -1 is 0',
  {
    instrs: [
      { id: 1, op: 'mod', dst: 0, a: { kind: 'imm', value: -2147483648 }, b: { kind: 'imm', value: -1 } },
      { id: 2, op: 'ret', src: { kind: 'reg', reg: 0 } },
    ],
  },
  1,
  'ok:0 trace=[1,2]',
);

// 4. 常量分支：只删确定不可达的一侧；确定出错指令之后的代码可删，但错误位置不变
check(
  'constant branch removes only dead side',
  {
    instrs: [
      { id: 1, op: 'const', dst: 0, value: 1 },
      { id: 2, op: 'br', cond: { kind: 'reg', reg: 0 }, thenLabel: 'T', elseLabel: 'E' },
      { id: 3, op: 'div', dst: 1, a: { kind: 'imm', value: 1 }, b: { kind: 'imm', value: 0 }, label: 'T' },
      { id: 4, op: 'jmp', target: 'M' },
      { id: 5, op: 'const', dst: 1, value: 42, label: 'E' },
      { id: 6, op: 'ret', src: { kind: 'reg', reg: 1 }, label: 'M' },
    ],
  },
  2,
  'err:div-by-zero@#3 trace=[1,2,3]',
);

// 5. 正常常量折叠：2+3*... 全程无错误，结果一致且轨迹 id 不变
check(
  'plain constant folding keeps trace ids',
  {
    instrs: [
      { id: 1, op: 'const', dst: 0, value: 2 },
      { id: 2, op: 'const', dst: 1, value: 3 },
      { id: 3, op: 'add', dst: 2, a: { kind: 'reg', reg: 0 }, b: { kind: 'reg', reg: 1 } },
      { id: 4, op: 'mul', dst: 3, a: { kind: 'reg', reg: 2 }, b: { kind: 'imm', value: 4 } },
      { id: 5, op: 'ret', src: { kind: 'reg', reg: 3 } },
    ],
  },
  4,
  'ok:20 trace=[1,2,3,4,5]',
);

// 6. 分支条件依赖 input（编译期未知）时，两侧都保留
{
  const prog: Program = {
    instrs: [
      { id: 1, op: 'input', dst: 0 },
      { id: 2, op: 'br', cond: { kind: 'reg', reg: 0 }, thenLabel: 'T', elseLabel: 'E' },
      { id: 3, op: 'const', dst: 1, value: 10, label: 'T' },
      { id: 4, op: 'jmp', target: 'M' },
      { id: 5, op: 'const', dst: 1, value: 20, label: 'E' },
      { id: 6, op: 'ret', src: { kind: 'reg', reg: 1 }, label: 'M' },
    ],
  };
  check('input-dependent branch keeps both sides', prog, 2, 'ok:10 trace=[1,2,3,4,6]', [7]);
  const opt = optimize(prog, 2);
  if (opt.program.instrs.length !== prog.instrs.length) {
    failures++;
    console.error('FAIL: input-dependent branch should not remove any instruction');
  }
}

// 7. x*0 中 x 来自 input 的除法：不能化简 x*0，运行期除零错误位置保持
{
  const prog: Program = {
    instrs: [
      { id: 1, op: 'input', dst: 0 },
      { id: 2, op: 'div', dst: 1, a: { kind: 'imm', value: 100 }, b: { kind: 'reg', reg: 0 } },
      { id: 3, op: 'mul', dst: 2, a: { kind: 'reg', reg: 1 }, b: { kind: 'imm', value: 0 } },
      { id: 4, op: 'ret', src: { kind: 'reg', reg: 2 } },
    ],
  };
  const opt = optimize(prog, 3);
  const r0 = run(prog, [0]);
  const r1 = run(opt.program, [0]);
  if (key(r0) !== 'err:div-by-zero@#2 trace=[1,2]' || key(r1) !== key(r0)) {
    failures++;
    console.error(`FAIL: x*0 with input divisor: orig=${key(r0)} opt=${key(r1)}`);
  } else {
    console.log('pass: x*0 with input-dependent x keeps div-by-zero at #2');
  }
  // 优化后 x*0 的 mul 与除法都必须保留（不能因 *0 消去对 x 的依赖）
  if (!opt.program.instrs.some((i) => i.id === 3 && i.op === 'mul')) {
    failures++;
    console.error('FAIL: optimizer must not simplify x*0 away');
  }
  const r2 = run(prog, [5]);
  const r3 = run(opt.program, [5]);
  if (key(r2) !== 'ok:0 trace=[1,2,3,4]' || key(r3) !== key(r2)) {
    failures++;
    console.error(`FAIL: x*0 nonzero input: orig=${key(r2)} opt=${key(r3)}`);
  } else {
    console.log('pass: x*0 with nonzero input evaluates to 0');
  }
}

if (failures > 0) {
  console.error(`${failures} targeted test(s) failed`);
  process.exit(1);
}
console.log('all targeted tests passed');
