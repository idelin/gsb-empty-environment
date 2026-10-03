/**
 * 运行样例：两人协同编辑文档 "AB"。
 * 用户1 在 A 后插入 'X'，用户2 在 A 后插入 'Y'，
 * 双向合并后两端收敛到同一结果（'AXYB' 或 'AYXB'，由标识序决定）。
 */
import { Rga, type RgaOp } from './rga.ts';

// 1. 两个副本从同一文档 'AB' 开始
const user1 = new Rga('user1');
const user2 = new Rga('user2');
const base: RgaOp[] = [
  user1.localInsert(0, 'A'),
  user1.localInsert(1, 'B'),
];
user2.merge(base);
console.log('初始文档  user1:', user1.toString(), ' user2:', user2.toString());

// 2. 离线并发编辑：两人都在 'A' 之后（可见下标 1）插入
const opX = user1.localInsert(1, 'X'); // user1: A X B
const opY = user2.localInsert(1, 'Y'); // user2: A Y B
console.log('并发编辑后 user1:', user1.toString(), ' user2:', user2.toString());

// 3. 交换操作（合并），两端收敛
user1.apply(opY);
user2.apply(opX);
console.log('合并后     user1:', user1.toString(), ' user2:', user2.toString());

// 4. 验证收敛
if (user1.toString() !== user2.toString()) {
  console.error('未收敛！');
  process.exit(1);
}
console.log('收敛结果:', user1.toString(), '（两端一致）');
