import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Network } from '../src/network.ts';
import { Endpoint, type DeliveryRecord, type EndpointOptions } from '../src/endpoint.ts';

function makePair(optsA: EndpointOptions = {}, optsB: EndpointOptions = {}) {
  const net = new Network();
  const a = new Endpoint('A', net, optsA);
  const b = new Endpoint('B', net, optsB);
  return { net, a, b };
}

function payloads(ep: Endpoint): string[] {
  return ep.delivered.map((d) => d.payload);
}

// 安全交付不变式：每一条交付给应用的数据，交付时本端必须已 ESTABLISHED，
// 且其代次等于已协商的对端代次，且每个代次内序号从 0 连续。
function assertDeliveryInvariant(ep: Endpoint) {
  const seqsByGen = new Map<number, number[]>();
  for (const d of ep.delivered) {
    assert.equal(d.state, 'ESTABLISHED', `${ep.id} 在非 ESTABLISHED 状态交付了数据`);
    assert.equal(d.gen, d.peerGen, `${ep.id} 交付了代次不符的数据`);
    const list = seqsByGen.get(d.gen) ?? [];
    list.push(d.seq);
    seqsByGen.set(d.gen, list);
  }
  for (const [gen, seqs] of seqsByGen) {
    seqs.forEach((s, i) => assert.equal(s, i, `${ep.id} gen=${gen} 序号不连续`));
  }
}

test('基本握手：三次握手完成后第一条数据安全交付', () => {
  const { net, a, b } = makePair();
  a.connect(b);
  net.run();
  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');

  a.sendData('hello');
  net.run();
  assert.deepEqual(payloads(b), ['hello']);
  assertDeliveryInvariant(a);
  assertDeliveryInvariant(b);
});

test('发送端重启序号归零：旧连接残包不被当作新数据', () => {
  const { net, a, b } = makePair();
  a.connect(b);
  net.run();

  a.sendData('old-0');
  a.sendData('old-1');
  a.sendData('old-2');
  net.holdWhere((m) => m.kind === 'DATA' && m.seq > 0); // old-1/old-2 滞留在网络中
  net.run();
  assert.deepEqual(payloads(b), ['old-0']);

  a.restart(); // 崩溃重启：gen 1 -> 2，序号归零
  net.run();
  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');

  a.sendData('new-0'); // 新连接序号从 0 重新开始
  net.run();
  assert.deepEqual(payloads(b), ['old-0', 'new-0']);

  net.releaseWhere((m) => m.kind === 'DATA'); // 旧残包此时才到达
  net.run();
  assert.deepEqual(payloads(b), ['old-0', 'new-0']); // 没有旧数据混入
  assert.equal(b.stats.staleData, 2);
  assertDeliveryInvariant(b);
});

test('对照实验：不校验代次时旧残包会被误收（演示危害）', () => {
  const { net, a, b } = makePair({}, { checkGeneration: false });
  a.connect(b);
  net.run();

  a.sendData('old-0');
  a.sendData('old-1');
  net.holdWhere((m) => m.kind === 'DATA' && m.seq === 1);
  net.run();

  a.restart();
  net.run();
  a.sendData('new-0');
  net.run();
  net.releaseWhere((m) => m.kind === 'DATA');
  net.run();

  // 朴素模式下，旧包 old-1 的序号恰好落在新连接的期望序号上，被误交付
  assert.deepEqual(payloads(b), ['old-0', 'new-0', 'old-1']);
});

test('确认丢失之一：RESET_ACK 丢失，靠重传完成握手', () => {
  const { net, a, b } = makePair();
  let dropAcks = 1;
  net.shouldDrop = (m) => m.kind === 'RESET_ACK' && dropAcks-- > 0; // 丢掉 B 的第一次应答
  a.connect(b);
  net.run();
  net.shouldDrop = null;

  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');
  assert.ok(a.stats.retransmits >= 1, 'A 应重传 RESET');

  a.sendData('after-loss');
  net.run();
  assert.deepEqual(payloads(b), ['after-loss']);
  assertDeliveryInvariant(b);
});

test('确认丢失之二：CONFIRM 丢失，首个数据隐式确认', () => {
  const { net, a, b } = makePair();
  a.connect(b);
  net.step(); // RESET 到达 B，B 进入 ACK_SENT
  net.step(); // RESET_ACK 到达 A，A 发出 CONFIRM 并 ESTABLISHED
  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ACK_SENT');
  net.dropWhere((m) => m.kind === 'CONFIRM'); // CONFIRM 丢失

  a.sendData('first-data'); // A 已 ESTABLISHED，直接发数据
  net.run();

  assert.equal(b.state, 'ESTABLISHED');
  assert.equal(b.stats.implicitConfirms, 1);
  assert.deepEqual(payloads(b), ['first-data']);
  assertDeliveryInvariant(b);
});

test('确认丢失之三：CONFIRM 丢失且无数据，靠 RESET_ACK 重传收敛', () => {
  const { net, a, b } = makePair();
  a.connect(b);
  net.step();
  net.step();
  net.dropWhere((m) => m.kind === 'CONFIRM');
  net.run(); // B 超时重传 RESET_ACK，A 重发 CONFIRM

  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');
  assert.ok(b.stats.retransmits >= 1, 'B 应重传 RESET_ACK');
});

test('双方同时重启：交叉握手收敛到同一代次对', () => {
  const { net, a, b } = makePair();
  a.connect(b);
  net.run();
  assert.equal(a.state, 'ESTABLISHED');

  a.restart(); // 双方“同时”重启：两个 RESET 都在途
  b.restart();
  net.run();

  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');
  assert.equal(a.negotiatedPeerGen, 2);
  assert.equal(b.negotiatedPeerGen, 2);

  a.sendData('a-to-b');
  b.sendData('b-to-a');
  net.run();
  assert.deepEqual(payloads(b), ['a-to-b']);
  assert.deepEqual(payloads(a), ['b-to-a']);
  assertDeliveryInvariant(a);
  assertDeliveryInvariant(b);
});

test('旧重置消息迟到：不破坏已建立的新连接', () => {
  const { net, a, b } = makePair();
  let holdResets = 1;
  let holdAcks = 1;
  net.shouldHold = (m) => {
    if (m.kind === 'RESET' && holdResets-- > 0) return true; // 扣留第一个 RESET（gen=1）
    if (m.kind === 'RESET_ACK' && holdAcks-- > 0) return true; // 扣留第一个 RESET_ACK（gen=1）
    return false;
  };
  a.connect(b);
  net.run(); // 靠重传完成 gen=1 握手
  net.shouldHold = null;
  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');

  a.sendData('gen1-data');
  net.run();
  assert.deepEqual(payloads(b), ['gen1-data']);

  a.restart(); // 进入 gen=2 并完成新握手
  net.run();
  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');
  assert.equal(b.negotiatedPeerGen, 2);

  // 旧握手报文此刻才姗姗来迟
  net.releaseWhere((m) => m.kind === 'RESET');
  net.releaseWhere((m) => m.kind === 'RESET_ACK');
  net.run();

  assert.equal(b.stats.staleReset, 1, '旧 RESET 应被识别并忽略');
  assert.ok(a.stats.staleHandshake >= 1, '旧 RESET_ACK 应被识别并忽略');
  assert.equal(a.state, 'ESTABLISHED');
  assert.equal(b.state, 'ESTABLISHED');

  a.sendData('gen2-data'); // 连接不受影响，数据照常交付
  net.run();
  assert.deepEqual(payloads(b), ['gen1-data', 'gen2-data']);
  assertDeliveryInvariant(b);
});

test('握手期间数据缓冲有上限：溢出丢弃，恢复后按序交付', () => {
  const { net, a, b } = makePair({}, { recvBufferCap: 2, retransmitMs: 1000, maxRetries: 10000 });
  net.shouldHold = (m) => m.kind === 'CONFIRM'; // CONFIRM 持续滞留，B 停留在握手期
  a.connect(b);
  net.step(); // RESET 到 B，B 进入 ACK_SENT
  net.step(); // RESET_ACK 到 A，A ESTABLISHED

  a.sendData('d0');
  a.sendData('d1');
  a.sendData('d2');
  a.sendData('d3');
  net.holdWhere((m) => m.kind === 'DATA' && m.seq === 0); // d0 也滞留，制造乱序
  net.run(); // d1、d2 进缓冲，d3 溢出

  assert.equal(b.state, 'ACK_SENT');
  assert.equal(b.stats.bufferDrops, 1);
  assert.deepEqual(payloads(b), []); // 握手未完成，什么都不交付

  net.shouldHold = null;
  net.releaseWhere((m) => m.kind === 'CONFIRM');
  net.step(); // B 收到 CONFIRM，ESTABLISHED
  assert.equal(b.state, 'ESTABLISHED');
  assert.deepEqual(payloads(b), []); // 缺 d0，缓冲中的 d1/d2 仍不能交付

  net.releaseWhere((m) => m.kind === 'DATA');
  net.run();
  assert.deepEqual(payloads(b), ['d0', 'd1', 'd2']); // d0 到达后按序释放
  assertDeliveryInvariant(b);
});

test('握手期间发送队列有上限：溢出丢弃，建连后补发', () => {
  const { net, a, b } = makePair({ sendQueueCap: 3 });
  a.connect(b); // A 进入 RESET_SENT，尚未 ESTABLISHED
  for (const p of ['q0', 'q1', 'q2', 'q3', 'q4']) a.sendData(p);
  assert.equal(a.stats.queueDrops, 2);

  net.run();
  assert.equal(a.state, 'ESTABLISHED');
  assert.deepEqual(payloads(b), ['q0', 'q1', 'q2']);
  assertDeliveryInvariant(b);
});

test('对端再次重启：旧代次缓冲在拆除时被清空', () => {
  const { net, a, b } = makePair();
  a.connect(b);
  net.run();

  a.sendData('g1-0');
  a.sendData('g1-1');
  net.holdWhere((m) => m.kind === 'DATA' && m.seq === 0); // g1-0 滞留，g1-1 进入乱序缓冲
  net.run();
  assert.deepEqual(payloads(b), []);

  a.restart(); // A 再次重启，B 拆除旧连接时丢弃缓冲的 g1-1
  net.run();
  a.sendData('g2-0');
  net.run();
  net.releaseWhere((m) => m.kind === 'DATA'); // 旧包 g1-0 到达
  net.run();

  assert.deepEqual(payloads(b), ['g2-0']);
  assert.equal(b.stats.staleData, 1);
  assertDeliveryInvariant(b);
});
