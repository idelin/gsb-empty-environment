#!/usr/bin/env node
/**
 * 用法: node src/cli.ts <model.json>
 * model.json 格式见 README.md。
 */

import { readFileSync } from 'node:fs';
import { generate } from './generate.ts';
import { renderReport } from './report.ts';

function main(): void {
  const file = process.argv[2];
  if (!file) {
    console.error('用法: node src/cli.ts <model.json>');
    process.exit(1);
  }
  let spec: unknown;
  try {
    spec = JSON.parse(readFileSync(file, 'utf8'));
  } catch (e) {
    console.error(`无法读取或解析输入文件 ${file}: ${(e as Error).message}`);
    process.exit(1);
  }
  try {
    const result = generate(spec as Parameters<typeof generate>[0]);
    console.log(renderReport(result));
  } catch (e) {
    console.error(`错误: ${(e as Error).message}`);
    process.exit(1);
  }
}

main();
