/**
 * 演示用工作进程：初始化账户 -> 提交批次 1 -> 批次 2 更新到一半时被父进程 SIGKILL。
 * 通过 stdout 汇报进度（行协议）：
 *   READY                 初始化完成
 *   COMMITTED A B         批次 1 已提交，A/B 为提交后的余额
 *   PROGRESS i            批次 2 第 i 笔更新已写入
 */
import { Database } from '../src/database.ts';

const dir = process.argv[2];
if (!dir) {
  console.error('usage: node examples/worker.ts <dir>');
  process.exit(1);
}

const db = Database.open(dir);

// 初始化两个账户并提交
const t0 = db.begin();
db.update(t0, 'accounts', 'A', 1000);
db.update(t0, 'accounts', 'B', 1000);
db.commit(t0);
db.flushAll();
console.log('READY');

// 批次 1：100 笔转账（A -> B，每笔 1 元），作为一个事务提交
const t1 = db.begin();
let a = 1000;
let b = 1000;
for (let i = 0; i < 100; i++) {
  a -= 1;
  b += 1;
  db.update(t1, 'accounts', 'A', a);
  db.update(t1, 'accounts', 'B', b);
}
db.commit(t1);
console.log(`COMMITTED ${a} ${b}`);

// 批次 2：继续转账，但永不提交 —— 进程将在中途被 SIGKILL（模拟断电）
const t2 = db.begin();
for (let i = 0; i < 100; i++) {
  a -= 1;
  b += 1;
  db.update(t2, 'accounts', 'A', a);
  db.update(t2, 'accounts', 'B', b);
  if (i === 20) db.flushPage('accounts'); // 模拟缓冲池置换：未提交脏页落盘（STEAL）
  console.log(`PROGRESS ${i + 1}`);
  // 给父进程留出 kill 窗口
  const until = Date.now() + 5;
  while (Date.now() < until);
}
console.log('SHOULD_NOT_REACH_HERE');
