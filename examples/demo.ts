/**
 * 端到端演示：
 *   1. 启动 worker 进程执行批量更新；
 *   2. 在批次 2 更新到一半时 SIGKILL worker（模拟突然断电）；
 *   3. 重启并执行 ARIES 恢复（Analysis -> Redo -> Undo）；
 *   4. 校验：已提交批次完整保留，未提交批次被回滚，数据无损坏。
 */
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Database } from '../src/database.ts';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'demo-data');
fs.rmSync(dir, { recursive: true, force: true });

console.log('=== 阶段 1：启动 worker，执行批量更新 ===');
const worker = spawn(process.execPath, ['examples/worker.ts', dir], {
  cwd: path.join(path.dirname(fileURLToPath(import.meta.url)), '..'),
  stdio: ['ignore', 'pipe', 'inherit'],
});

let committedA = 0;
let committedB = 0;
let killed = false;

const done = new Promise<void>((resolve) => {
  worker.stdout!.on('data', (chunk: Buffer) => {
    for (const line of chunk.toString().split('\n')) {
      if (line.startsWith('COMMITTED')) {
        [, committedA, committedB] = line.split(' ').map(Number) as never;
        console.log(`  批次 1 已提交：A=${committedA}, B=${committedB}`);
      } else if (line.startsWith('PROGRESS')) {
        const n = Number(line.split(' ')[1]);
        if (n === 40 && !killed) {
          killed = true;
          console.log(`=== 阶段 2：批次 2 进行到第 ${n} 笔，模拟突然断电（SIGKILL）===`);
          worker.kill('SIGKILL');
        }
      }
    }
  });
  worker.on('exit', (code, signal) => {
    console.log(`  worker 退出：code=${code}, signal=${signal}`);
    resolve();
  });
});
await done;

console.log('=== 阶段 3：重启，执行 ARIES 恢复 ===');
const { db, report } = Database.recover(dir);
console.log(`  Analysis：checkpointLSN=${report.analysis.checkpointLSN}, ` +
  `winners=[${report.analysis.winners}], losers=[${report.analysis.losers}], ` +
  `DPT=${JSON.stringify(report.analysis.dpt)}`);
console.log(`  Redo    ：从 LSN=${report.redo.fromLSN} 起重放 ${report.redo.replayed} 条记录`);
console.log(`  Undo    ：回滚 loser 事务 [${report.undo.losers}]，写入 ${report.undo.clrCount} 条 CLR`);

console.log('=== 阶段 4：校验恢复结果 ===');
const a = db.read('accounts', 'A');
const b = db.read('accounts', 'B');
console.log(`  恢复后余额：A=${a}, B=${b}`);
console.log(`  期望余额  ：A=${committedA}, B=${committedB}（批次 1 提交点）`);

const ok = a === committedA && b === committedB && (a as number) + (b as number) === 2000;
console.log(ok ? '  ✅ 恢复成功：已提交数据完整，未提交数据已回滚，账目平衡' : '  ❌ 恢复失败');
db.close();
process.exit(ok ? 0 : 1);
