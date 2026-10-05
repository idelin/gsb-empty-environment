import { readFileSync } from 'node:fs';
import { findOptimalScheme } from './anonymize.ts';
import type { Dataset } from './types.ts';

function printUsage(): void {
  console.error(
    '用法: node src/cli.ts <输入.json> [--k 3]\n' +
      '输入 JSON 结构: { "k"?: number, "columns": [...], "hierarchies": {...}, "rows": [...] }',
  );
}

function main(argv: string[]): number {
  const args = [...argv];
  let inputPath: string | undefined;
  let kOverride: number | undefined;
  while (args.length > 0) {
    const arg = args.shift() as string;
    if (arg === '--k') {
      const raw = args.shift();
      kOverride = raw === undefined ? NaN : Number(raw);
      if (!Number.isInteger(kOverride) || (kOverride as number) < 2) {
        console.error(`--k 需要不小于 2 的整数，收到：${raw}`);
        return 2;
      }
    } else if (arg === '--help' || arg === '-h') {
      printUsage();
      return 0;
    } else if (inputPath === undefined) {
      inputPath = arg;
    } else {
      console.error(`无法识别的参数：${arg}`);
      printUsage();
      return 2;
    }
  }
  if (inputPath === undefined) {
    printUsage();
    return 2;
  }

  const dataset = JSON.parse(readFileSync(inputPath, 'utf8')) as Dataset & {
    k?: number;
  };
  const k = kOverride ?? dataset.k ?? 3;
  const result = findOptimalScheme(dataset, k);

  if (!result.feasible) {
    console.log(`【无解】${result.message}`);
    if (result.violatingGroups && result.violatingGroups.length > 0) {
      console.log('最高层级下仍违规的分组：');
      for (const group of result.violatingGroups) {
        console.log(`  ${JSON.stringify(group.values)} -> ${group.count} 人`);
      }
    }
    return 1;
  }

  console.log(`【可行方案】k=${result.k}，统一列级层级：`);
  for (const [column, level] of Object.entries(result.levels)) {
    console.log(`  ${column}: 第 ${level} 层`);
  }
  console.log(`\n分组计数（共 ${result.groupCount} 组，最小组 ${result.minGroupSize} 人）：`);
  for (const group of result.groups) {
    console.log(`  ${JSON.stringify(group.values)} -> ${group.count} 人`);
  }
  console.log(
    `\n损失：总泛化损失 = ${result.totalLoss.toFixed(4)}，` +
      `归一化平均损失 = ${result.averageLoss.toFixed(4)}`,
  );
  return 0;
}

process.exit(main(process.argv.slice(2)));
