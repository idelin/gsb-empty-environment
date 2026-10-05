import { readFileSync } from 'node:fs';
import { validateTable } from './model.ts';
import { analyzeTable } from './analyze.ts';
import { formatReport, toJsonReport } from './report.ts';

function main(argv: string[]): number {
  const jsonMode = argv.includes('--json');
  const file = argv.find((a) => !a.startsWith('--'));
  if (!file) {
    console.error('用法: node src/cli.ts <table.json> [--json]');
    return 1;
  }

  let table;
  try {
    table = validateTable(JSON.parse(readFileSync(file, 'utf8')));
  } catch (err) {
    console.error(`加载失败: ${(err as Error).message}`);
    return 1;
  }

  const report = analyzeTable(table);
  if (jsonMode) {
    console.log(JSON.stringify(toJsonReport(report, table.fields), null, 2));
  } else {
    console.log(formatReport(report, table.fields));
  }
  return 0;
}

process.exit(main(process.argv.slice(2)));
