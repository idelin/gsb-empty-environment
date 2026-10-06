import { run, RunResult } from './interp';
import { optimize } from './optimize';
import { genProgram } from './gen';
import { formatProgram } from './ir';

/**
 * 差分测试：对同一随机程序，分别用原解释器与优化后解释器执行，
 * 比较最终结果与完整错误轨迹（执行指令 id 序列 + 错误类别 + 出错指令 id）。
 */

function outcomeKey(r: RunResult): string {
  const o = r.outcome;
  return o.ok ? `ok:${o.value}` : `err:${o.error}@#${o.instrId}`;
}

function compare(seed: number): { mismatch: boolean; detail?: string; stats: Stats } {
  const { program, numRegs, inputs } = genProgram(seed);
  const original = run(program, inputs);
  const opt = optimize(program, numRegs);
  const optimized = run(opt.program, inputs);

  const stats: Stats = {
    instrs: program.instrs.length,
    removed: opt.removed,
    folded: opt.folded,
    branchesFolded: opt.branchesFolded,
    hadError: !original.outcome.ok,
  };

  const sameTrace =
    original.trace.length === optimized.trace.length &&
    original.trace.every((id, i) => id === optimized.trace[i]);
  const sameOutcome = outcomeKey(original) === outcomeKey(optimized);

  if (sameTrace && sameOutcome) return { mismatch: false, stats };

  const detail = [
    `seed=${seed}`,
    `outcome: original=${outcomeKey(original)} optimized=${outcomeKey(optimized)}`,
    `trace original : [${original.trace.join(',')}]`,
    `trace optimized: [${optimized.trace.join(',')}]`,
    '--- original program ---',
    formatProgram(program),
    '--- optimized program ---',
    formatProgram(opt.program),
  ].join('\n');
  return { mismatch: true, detail, stats };
}

interface Stats {
  instrs: number;
  removed: number;
  folded: number;
  branchesFolded: number;
  hadError: boolean;
}

function main(): void {
  const count = Number(process.argv[2] ?? 2000);
  const seedStart = Number(process.argv[3] ?? 1);
  let failures = 0;
  let errors = 0;
  let totalRemoved = 0;
  let totalFolded = 0;
  let totalBranches = 0;
  let totalInstrs = 0;

  for (let i = 0; i < count; i++) {
    const seed = seedStart + i;
    const res = compare(seed);
    totalInstrs += res.stats.instrs;
    totalRemoved += res.stats.removed;
    totalFolded += res.stats.folded;
    totalBranches += res.stats.branchesFolded;
    if (res.stats.hadError) errors++;
    if (res.mismatch) {
      failures++;
      console.error(`MISMATCH\n${res.detail}\n`);
      if (failures >= 5) {
        console.error('too many failures, stopping early');
        process.exit(1);
      }
    }
  }

  console.log(`programs tested : ${count}`);
  console.log(`with runtime err: ${errors}`);
  console.log(`mismatches      : ${failures}`);
  console.log(
    `optimizer stats : removed=${totalRemoved} folded=${totalFolded} ` +
      `branches-folded=${totalBranches} avg-instrs=${(totalInstrs / count).toFixed(1)}`,
  );
  if (failures > 0) process.exit(1);
  console.log('OK: 原解释器与优化后解释器的结果及错误轨迹完全一致');
}

main();
