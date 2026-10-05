// 全部核验入口：转译自检 -> 单元测试 -> 穷举 -> 随机对拍

import * as fs from 'fs';
import * as path from 'path';
import * as childProcess from 'child_process';
import { run as runUnit } from './unit';
import { run as runExhaustive } from './exhaustive';
import { run as runRandom } from './random';

function transpileSelfCheck(): void {
  // 用 tsrun 的 transform 转译所有 .ts，再逐个 node --check 验证语法
  const tsrun = require('../scripts/tsrun.js');
  const outDir = path.resolve(__dirname, '../.build');
  fs.rmSync(outDir, { recursive: true, force: true });
  const dirs = [path.resolve(__dirname, '../src'), path.resolve(__dirname, '../test')];
  let count = 0;
  for (const dir of dirs) {
    for (const name of fs.readdirSync(dir)) {
      if (!name.endsWith('.ts')) continue;
      const file = path.join(dir, name);
      const out = tsrun.transform(fs.readFileSync(file, 'utf8'), file);
      const outFile = path.join(outDir, name.replace(/\.ts$/, '.js'));
      fs.mkdirSync(path.dirname(outFile), { recursive: true });
      fs.writeFileSync(outFile, out);
      childProcess.execFileSync(process.execPath, ['--check', outFile]);
      count++;
    }
  }
  console.log(`transpile-check: ${count} 个文件转译并通过 node --check`);
}

transpileSelfCheck();
runUnit();
runExhaustive();
runRandom();
console.log('全部核验通过');
