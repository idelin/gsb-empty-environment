import { readFileSync, writeFileSync } from 'node:fs';
import { migrateCoverage } from './migrate.ts';
import { formatTextReport } from './report.ts';
import type { NewStatementsFile, OldCoverageFile } from './types.ts';
import type { RulesFile } from './rules.ts';

interface CliOptions {
  oldPath: string;
  newPath: string;
  rulesPath?: string;
  outPath?: string;
  format: 'json' | 'text';
}

const USAGE = `用法: node src/cli.ts --old <旧覆盖.json> --new <新语句.json> [--rules <映射规则.json>] [--out <报告.json>] [--format json|text]

输入格式见 README.md。退出码：存在不确定项时为 1，否则为 0。`;

export function parseArgs(argv: string[]): CliOptions {
  const options: Partial<CliOptions> = { format: 'json' };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = () => {
      const value = argv[++i];
      if (value === undefined) throw new Error(`参数 ${arg} 缺少取值`);
      return value;
    };
    switch (arg) {
      case '--old': options.oldPath = next(); break;
      case '--new': options.newPath = next(); break;
      case '--rules': options.rulesPath = next(); break;
      case '--out': options.outPath = next(); break;
      case '--format': {
        const value = next();
        if (value !== 'json' && value !== 'text') throw new Error(`未知格式: ${value}`);
        options.format = value;
        break;
      }
      default:
        throw new Error(`未知参数: ${arg}`);
    }
  }
  if (!options.oldPath || !options.newPath) {
    throw new Error('--old 与 --new 为必填参数');
  }
  return options as CliOptions;
}

function readJson<T>(path: string): T {
  return JSON.parse(readFileSync(path, 'utf8')) as T;
}

export function run(argv: string[]): number {
  const options = parseArgs(argv);
  const oldFile = readJson<OldCoverageFile>(options.oldPath);
  const newFile = readJson<NewStatementsFile>(options.newPath);
  const rules = options.rulesPath ? readJson<RulesFile>(options.rulesPath).rules : [];

  const report = migrateCoverage(oldFile.statements, newFile.statements, rules);
  const output = options.format === 'text'
    ? formatTextReport(report)
    : JSON.stringify(report, null, 2);

  if (options.outPath) {
    writeFileSync(options.outPath, output + '\n', 'utf8');
    console.error(`报告已写入 ${options.outPath}`);
  } else {
    console.log(output);
  }
  return report.uncertain.length > 0 ? 1 : 0;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    process.exitCode = run(process.argv.slice(2));
  } catch (error) {
    console.error(`${(error as Error).message}\n\n${USAGE}`);
    process.exitCode = 2;
  }
}
