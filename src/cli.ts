import { readFileSync } from 'fs';
import { runCheck } from './checker';
import { InputError, normalizeInput } from './model';
import { renderReport } from './report';

function usage(): void {
  console.error('用法: node cli.js [--json] <input.json | ->');
  console.error('  input.json 结构: { "attributes": [...], "fds": [...], "decomposition": [[...], ...] }');
  console.error('  fds 支持对象形式 {"lhs": ["A"], "rhs": ["B"]} 或字符串形式 "A->B"');
}

function main(): void {
  const args = process.argv.slice(2);
  let jsonMode = false;
  const positional: string[] = [];
  for (const arg of args) {
    if (arg === '--json') {
      jsonMode = true;
    } else {
      positional.push(arg);
    }
  }
  if (positional.length !== 1) {
    usage();
    process.exit(2);
  }
  const source = positional[0];
  let text: string;
  try {
    text = source === '-' ? readFileSync(0, 'utf8') : readFileSync(source, 'utf8');
  } catch (err) {
    console.error(`无法读取输入文件 ${source}: ${String(err)}`);
    process.exit(1);
  }
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    console.error(`输入不是合法 JSON: ${String(err)}`);
    process.exit(1);
  }
  try {
    const schema = normalizeInput(raw as never);
    const result = runCheck(schema);
    if (jsonMode) {
      console.log(JSON.stringify(result, null, 2));
    } else {
      console.log(renderReport(result));
    }
    process.exit(result.determinism.ok ? 0 : 3);
  } catch (err) {
    if (err instanceof InputError) {
      console.error(`输入错误: ${err.message}`);
      process.exit(1);
    }
    throw err;
  }
}

main();
