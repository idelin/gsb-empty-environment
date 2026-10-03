/**
 * 运行样例：两人协同编辑文档 'AB'
 *   用户1 在 A 后插入 'X'，用户2 并发在 A 后插入 'Y'
 *   合并后两端稳定展示同一结果（本实现为 'AYXB'），绝无交错。
 */
import { RGA, type Op } from "../src/rga.ts";

// 1. 初始文档 'AB'（由站点 init 写入）
const init = new RGA("init");
const initOps: Op[] = [init.insert(0, "A"), init.insert(1, "B")];

// 2. 两个用户各自拿到文档副本
const user1 = new RGA("u1");
const user2 = new RGA("u2");
for (const op of initOps) {
  user1.apply(op);
  user2.apply(op);
}
console.log(`初始文档:  user1="${user1}"  user2="${user2}"`);

// 3. 双方离线并发编辑：都在 'A' 之后插入
const opX = user1.insert(1, "X"); // user1: A -> AXB
const opY = user2.insert(1, "Y"); // user2: A -> AYB
console.log(`并发编辑:  user1="${user1}" (插入 X, id=${opX.id.counter}@${opX.id.site})`);
console.log(`            user2="${user2}" (插入 Y, id=${opY.id.counter}@${opY.id.site})`);

// 4. 网络恢复，互相同步操作
user2.apply(opX);
user1.apply(opY);

// 5. 两端收敛到同一稳定结果
console.log(`合并结果:  user1="${user1}"  user2="${user2}"`);
console.log(user1.toString() === user2.toString() ? "✔ 两端一致，无交错、无乱序" : "✘ 未收敛！");
