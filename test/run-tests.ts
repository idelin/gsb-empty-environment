import { runChase } from '../src/chase';
import { runCheck } from '../src/checker';
import { InputError, normalizeInput, Schema } from '../src/model';
import { checkPreservation } from '../src/preservation';

let passed = 0;
let failed = 0;

function check(name: string, cond: boolean, detail?: string): void {
  if (cond) {
    passed++;
    console.log(`  ok - ${name}`);
  } else {
    failed++;
    console.log(`  FAIL - ${name}${detail ? `: ${detail}` : ''}`);
  }
}

function makeSchema(raw: {
  attributes: string[];
  fds: Array<string | { lhs: string[]; rhs: string[] }>;
  decomposition: string[][];
}): Schema {
  return normalizeInput(raw);
}

console.log('[用例1] R(ABC), F={A→B,B→C}, ρ={AB,BC}: 无损且保持依赖');
{
  const schema = makeSchema({
    attributes: ['A', 'B', 'C'],
    fds: ['A->B', 'B->C'],
    decomposition: [
      ['A', 'B'],
      ['B', 'C'],
    ],
  });
  const result = runCheck(schema);
  check('无损连接成立', result.chase.lossless);
  check('见证行为 r1', result.chase.witnessRow === 0);
  check('仅 1 步合并', result.chase.steps.length === 1);
  const step = result.chase.steps[0];
  check('步骤1 由 FD#2 触发', step.fdIndex === 1);
  check('步骤1 b1,3 并入 a3（distinguished 优先）',
    step.absorbed.kind === 'nondistinguished' && step.into.kind === 'distinguished');
  check('依赖保持成立', result.preservation.preserved);
  check('可重放自检通过', result.determinism.ok);
}

console.log('[用例2] R(ABC), F={A→B,B→C}, ρ={AB,AC}: 无损但不保持依赖');
{
  const schema = makeSchema({
    attributes: ['A', 'B', 'C'],
    fds: ['A->B', 'B->C'],
    decomposition: [
      ['A', 'B'],
      ['A', 'C'],
    ],
  });
  const result = runCheck(schema);
  check('无损连接成立', result.chase.lossless);
  check('合并 2 步', result.chase.steps.length === 2);
  check('依赖保持不成立', !result.preservation.preserved);
  const bc = result.preservation.perFd[1];
  check('B→C 不保持', !bc.preserved);
  check('闭包缺口恰为 {C}', bc.gap.length === 1 && bc.gap[0] === 2);
  check('可达闭包仅为 {B}', bc.reachableClosure.length === 1 && bc.reachableClosure[0] === 1);
  check('A→B 仍保持', result.preservation.perFd[0].preserved);
}

console.log('[用例3] R(ABC), F={A→C}, ρ={AB,BC}: 有损且不保持依赖');
{
  const schema = makeSchema({
    attributes: ['A', 'B', 'C'],
    fds: ['A->C'],
    decomposition: [
      ['A', 'B'],
      ['B', 'C'],
    ],
  });
  const result = runCheck(schema);
  check('无损连接不成立', !result.chase.lossless);
  check('无任何合并步骤', result.chase.steps.length === 0);
  check('依赖保持不成立，缺口 {C}',
    !result.preservation.preserved &&
      result.preservation.perFd[0].gap.length === 1 &&
      result.preservation.perFd[0].gap[0] === 2);
}

console.log('[用例4] R(ABC), F={A→B}, ρ={AB,BC}: 保持依赖但有损（保持≠无损）');
{
  const schema = makeSchema({
    attributes: ['A', 'B', 'C'],
    fds: ['A->B'],
    decomposition: [
      ['A', 'B'],
      ['B', 'C'],
    ],
  });
  const result = runCheck(schema);
  check('依赖保持成立', result.preservation.preserved);
  check('无损连接不成立（保持依赖不能当作无损）', !result.chase.lossless);
}

console.log('[用例5] 教科书五路分解: R(ABCDE), ρ={AD,AB,BE,CDE,AE}: 无损但不保持');
{
  const schema = makeSchema({
    attributes: ['A', 'B', 'C', 'D', 'E'],
    fds: ['A->C', 'B->C', 'C->D', 'DE->C', 'CE->A'],
    decomposition: [
      ['A', 'D'],
      ['A', 'B'],
      ['B', 'E'],
      ['C', 'D', 'E'],
      ['A', 'E'],
    ],
  });
  const result = runCheck(schema);
  check('无损连接成立', result.chase.lossless);
  check('见证行为 r3 (BE)', result.chase.witnessRow === 2);
  check('共 9 步合并', result.chase.steps.length === 9, `实际 ${result.chase.steps.length}`);
  check('第 9 步首次出现全 a 行', result.chase.witnessFirstStep === 9);
  const first = result.chase.steps[0];
  check('步骤1 非 distinguished 合并取行号小者 (b2,3→b1,3)',
    first.absorbed.kind === 'nondistinguished' &&
      first.absorbed.row === 1 &&
      first.into.kind === 'nondistinguished' &&
      first.into.row === 0);
  check('依赖保持不成立', !result.preservation.preserved);
  const perFd = result.preservation.perFd;
  check('A→C 缺口 {C}', !perFd[0].preserved && perFd[0].gap.join() === '2');
  check('B→C 缺口 {C}', !perFd[1].preserved && perFd[1].gap.join() === '2');
  check('C→D 保持', perFd[2].preserved);
  check('DE→C 保持', perFd[3].preserved);
  check('CE→A 缺口 {A}', !perFd[4].preserved && perFd[4].gap.join() === '0');
  check('可重放自检通过', result.determinism.ok);
}

console.log('[用例6] 确定性：同一输入两次检查结果完全一致');
{
  const schema = makeSchema({
    attributes: ['A', 'B', 'C', 'D', 'E'],
    fds: ['A->C', 'B->C', 'C->D', 'DE->C', 'CE->A'],
    decomposition: [
      ['A', 'D'],
      ['A', 'B'],
      ['B', 'E'],
      ['C', 'D', 'E'],
      ['A', 'E'],
    ],
  });
  const first = JSON.stringify(runCheck(schema).chase.steps);
  const second = JSON.stringify(runCheck(schema).chase.steps);
  check('两次追逐步骤序列一致', first === second);
}

console.log('[用例7] 输入校验');
{
  const expectError = (name: string, fn: () => void): void => {
    try {
      fn();
      check(name, false, '未抛出 InputError');
    } catch (err) {
      check(name, err instanceof InputError, String(err));
    }
  };
  expectError('超过 10 个属性被拒绝', () =>
    makeSchema({
      attributes: ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K'],
      fds: [],
      decomposition: [['A']],
    }),
  );
  expectError('FD 引用未知属性被拒绝', () =>
    makeSchema({ attributes: ['A', 'B'], fds: ['A->Z'], decomposition: [['A', 'B']] }),
  );
  expectError('分解未覆盖全部属性被拒绝', () =>
    makeSchema({ attributes: ['A', 'B', 'C'], fds: [], decomposition: [['A', 'B']] }),
  );
  expectError('重复属性名被拒绝', () =>
    makeSchema({ attributes: ['A', 'A'], fds: [], decomposition: [['A']] }),
  );
}

console.log('[用例8] 单属性子模式与空 FD 集');
{
  const schema = makeSchema({
    attributes: ['A', 'B'],
    fds: [],
    decomposition: [['A'], ['B']],
  });
  const chase = runChase(schema);
  check('无 FD 且分解不含全集 → 有损', !chase.lossless);
  const trivial = makeSchema({
    attributes: ['A', 'B'],
    fds: [],
    decomposition: [['A', 'B'], ['A']],
  });
  check('子模式含全集 → 无损（初始即有全 a 行）', runChase(trivial).lossless);
  const preservation = checkPreservation(trivial);
  check('空 FD 集 → 依赖保持平凡成立', preservation.preserved);
}

console.log('');
console.log(`通过 ${passed}，失败 ${failed}`);
if (failed > 0) {
  process.exitCode = 1;
}
