import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  PausableQueue,
  PortCapabilityError,
  PortHost,
  type Domain,
  type IncomingMessage,
  type PortErrorCode,
} from '../src/port-capability.ts';

function assertCode(fn: () => unknown, code: PortErrorCode): void {
  assert.throws(fn, (err: unknown) => {
    assert.ok(err instanceof PortCapabilityError, `expected PortCapabilityError, got ${err}`);
    assert.equal(err.code, code);
    return true;
  });
}

function inbox(domain: Domain): IncomingMessage[] {
  const received: IncomingMessage[] = [];
  domain.onmessage = (message) => received.push(message);
  return received;
}

test('转移与消息入队原子完成,旧句柄立即失效', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  const yInbox = inbox(y);
  const { port1: a } = host.createChannel('X', 'Y');
  const { port1: c } = host.createChannel('X', 'Y');

  host.postMessage(x, a, 'take-c', [c]);

  assert.equal(c.ownerId, 'Y'); // 入队的同时归属已变更
  assertCode(() => host.postMessage(x, c, 'stale'), 'NOT_OWNER'); // 旧句柄失效

  assert.equal(host.pump('Y'), 1);
  assert.equal(yInbox.length, 1);
  assert.equal(yInbox[0].payload, 'take-c');
  assert.deepEqual([...yInbox[0].transfer], [c]);
  host.postMessage(y, c, 'c-is-mine'); // 新持有者可以正常使用
});

test('批次最后一个端口无效:整体拒绝,归属与队列保持原状', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  const { port1: a, port2: b } = host.createChannel('X', 'Y');
  const { port1: c1 } = host.createChannel('X', 'Y');
  const { port1: c2 } = host.createChannel('X', 'Y');

  b.mailbox.pause(); // 即使在暂停的邮箱上,失败也不许留下任何痕迹
  // 前两个有效、最后一个 b 属于 Y,不属于 X
  assertCode(() => host.postMessage(x, a, 'batch', [c1, c2, b]), 'NOT_OWNER');

  assert.equal(b.mailbox.size, 0); // 消息没有入队
  assert.equal(c1.ownerId, 'X'); // 失败保持原归属
  assert.equal(c2.ownerId, 'X');
  host.postMessage(x, c1, 'still-mine'); // 旧句柄仍然有效
});

test('重复转移同一端口被拒绝', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  host.createDomain('Y');
  const { port1: a } = host.createChannel('X', 'Y');
  const { port1: c } = host.createChannel('X', 'Y');

  // 同一批次内重复
  assertCode(() => host.postMessage(x, a, 'dup', [c, c]), 'DUPLICATE_TRANSFER');
  assert.equal(c.ownerId, 'X');

  // 成功转移后再次转移(句柄已失效)
  host.postMessage(x, a, 'take-c', [c]);
  assertCode(() => host.postMessage(x, a, 'again', [c]), 'NOT_OWNER');
  assert.equal(c.ownerId, 'Y');
});

test('不能通过端口转移它自己', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  host.createDomain('Y');
  const { port1: a } = host.createChannel('X', 'Y');

  assertCode(() => host.postMessage(x, a, 'self', [a]), 'SELF_TRANSFER');
  assert.equal(a.ownerId, 'X');
});

test('转移与关闭交错:已入队的转移在关闭后依然有效', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  const yInbox = inbox(y);
  const { port1: a, port2: b } = host.createChannel('X', 'Y');
  const { port1: c, port2: d } = host.createChannel('X', 'Y');

  b.mailbox.pause(); // 暂停 Y 侧接收,让转移停留在"已入队未投递"
  host.postMessage(x, a, 'take-c', [c]); // 转移 + 入队原子提交
  host.closeDomain('X'); // 关闭与在途转移交错

  assert.equal(c.ownerId, 'Y'); // 归属在入队时已转移,不受关闭影响
  assert.ok(c.isAlive && d.isAlive); // 在途转移的端口仍可达,不被回收
  assert.ok(a.isAlive); // a 的对端 b 仍可达,a 成为无主悬空端点
  assert.equal(a.ownerId, null);
  assertCode(() => host.postMessage(x, a, 'too-late'), 'DOMAIN_CLOSED');

  b.mailbox.resume();
  assert.equal(host.pump('Y'), 1); // 恢复后投递给新持有者
  assert.equal(yInbox.length, 1);
  assert.deepEqual([...yInbox[0].transfer], [c]);
  host.postMessage(y, c, 'c-works'); // Y 可以使用在途获得的端口
});

test('迟到回复:邮箱跟随端口,投递给当前持有者', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  const z = host.createDomain('Z');
  const xInbox = inbox(x);
  const zInbox = inbox(z);
  const { port1: a, port2: b } = host.createChannel('X', 'Y');
  const { port1: e } = host.createChannel('X', 'Z');

  // X 提问,Y 收到后先不回复
  let answer: ((payload: unknown) => void) | null = null;
  y.onmessage = (message) => {
    answer = (payload) => message.reply(payload);
  };
  host.postMessage(x, a, 'question');
  host.pump('Y');
  assert.ok(answer);

  a.mailbox.pause(); // 暂停 X 侧的回复通道
  answer!('late-answer'); // 回复已入队但尚未投递
  host.postMessage(x, e, 'hand-a-to-z', [a]); // X 把接收端 a 转给 Z
  assert.equal(a.ownerId, 'Z');

  a.mailbox.resume();
  host.pump();
  assert.equal(xInbox.length, 0); // 原持有者收不到
  assert.ok(zInbox.some((message) => message.payload === 'late-answer')); // 新持有者收到
});

test('迟到回复:目标域已关闭则滞留,随组件一起回收', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  inbox(x);
  const { port1: a, port2: b } = host.createChannel('X', 'Y');

  a.mailbox.pause();
  host.postMessage(y, b, 'reply-to-dead'); // 回复在途
  host.closeDomain('X'); // 目标域关闭,a 成为悬空端点

  a.mailbox.resume();
  assert.equal(host.pump(), 0); // 无人接收:不投递、不报错
  assert.ok(a.isAlive); // 组件仍可达(b 在 Y),a 未被回收
  assert.equal(a.mailbox.size, 1); // 回复滞留在悬空邮箱中

  host.closeDomain('Y'); // 整个组件失去可达性
  assert.ok(!a.isAlive && !b.isAlive); // 连同滞留消息一并回收
  assert.equal(a.mailbox.size, 0);
});

test('端口可以成环,关闭后整个环被标记-清除回收', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const { port1: a1, port2: a2 } = host.createChannel('X');
  const { port1: b1, port2: b2 } = host.createChannel('X');

  // 构造环:a2 的队列持有 b1,b1 的对端是 b2,b2 的队列持有 a1,a1 的对端是 a2
  host.postMessage(x, a1, 'b1-inside', [b1]);
  host.postMessage(x, b1, 'a1-inside', [a1]);

  host.closeDomain('X'); // 环上所有端口都失去外部可达性
  assert.ok(!a1.isAlive && !a2.isAlive && !b1.isAlive && !b2.isAlive);
  assert.equal(host.alivePorts.length, 0);
});

test('跨域环在仍有外部根时存活,根消失后整体回收', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  const { port1: a1, port2: a2 } = host.createChannel('X', 'Y');
  const { port1: b1, port2: b2 } = host.createChannel('X', 'Y');

  // 互相在途转移,形成 a2 -> b1 -> a2 的环(另有 a1<->a2、b1<->b2 纠缠边)
  host.postMessage(x, a1, 'b1-inside', [b1]);
  host.postMessage(y, b2, 'a2-inside', [a2]);

  host.closeDomain('X'); // Y 仍持有环上的端口,整个组件存活
  assert.ok(a1.isAlive && a2.isAlive && b1.isAlive && b2.isAlive);
  assert.equal(a1.ownerId, null); // a1 成为无主悬空端点

  host.closeDomain('Y'); // 外部根消失,环整体回收
  assert.ok(!a1.isAlive && !a2.isAlive && !b1.isAlive && !b2.isAlive);
  assert.equal(host.alivePorts.length, 0);
});

test('关闭域只回收不可达端口,有外部根的组件存活', () => {
  const host = new PortHost();
  const x = host.createDomain('X');
  const y = host.createDomain('Y');
  const { port1: a, port2: b } = host.createChannel('X', 'Y'); // 有外部根 b
  const { port1: orphan } = host.createChannel('X'); // 两端都在 X,无外部根

  host.closeDomain('X');

  assert.ok(a.isAlive && b.isAlive); // 组件经 b 可达,存活
  assert.equal(a.ownerId, null); // a 成为无主悬空端点
  assert.ok(!orphan.isAlive); // 不可达组件被回收
  assert.equal(host.alivePorts.length, 2);
});

test('可暂停队列:暂停缓冲、恢复后按 FIFO 取出', () => {
  const queue = new PausableQueue<number>();
  queue.enqueue(1);
  assert.equal(queue.dequeue(), 1);

  queue.pause();
  queue.enqueue(2);
  queue.enqueue(3);
  assert.equal(queue.dequeue(), undefined); // 暂停期间取不出
  assert.equal(queue.size, 2);

  queue.resume();
  assert.equal(queue.dequeue(), 2);
  assert.equal(queue.dequeue(), 3);
  assert.equal(queue.dequeue(), undefined);

  queue.enqueue(4);
  assert.deepEqual(queue.clear(), [4]); // 清空并返回被丢弃的元素
  assert.equal(queue.size, 0);
});
