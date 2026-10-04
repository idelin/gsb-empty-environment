// 场景演示：连接重置模拟器
// 运行：node src/simulate.ts（需 Node >= 22.6，推荐 23）

import { Network } from './network.ts';
import { Endpoint } from './endpoint.ts';

function banner(title: string): void {
  console.log(`\n${'='.repeat(64)}\n${title}\n${'='.repeat(64)}`);
}

function showDelivered(ep: Endpoint): void {
  const items = ep.delivered.map((d) => `gen=${d.gen} seq=${d.seq} "${d.payload}"`);
  console.log(`  ${ep.id} 已交付给应用: [${items.join(', ')}]`);
}

function showStats(ep: Endpoint): void {
  const s = Object.entries(ep.stats)
    .filter(([, v]) => v > 0)
    .map(([k, v]) => `${k}=${v}`)
    .join(', ');
  console.log(`  ${ep.id} 统计: ${s || '(无异常)'}`);
}

// ---------------------------------------------------------------------------
banner('场景 1：发送端重启、序号归零 —— 旧连接残包被拒收');
{
  const net = new Network();
  const a = new Endpoint('A', net);
  const b = new Endpoint('B', net);
  a.connect(b);
  net.run();
  console.log(`握手完成：A gen=${a.disk.gen}, B gen=${b.disk.gen}`);

  a.sendData('old-0');
  a.sendData('old-1');
  a.sendData('old-2');
  net.holdWhere((m) => m.kind === 'DATA' && m.seq > 0); // old-1/old-2 滞留网络
  net.run();
  showDelivered(b);

  console.log('-- A 崩溃重启：gen 1 -> 2，发送序号归零 --');
  a.restart();
  net.run();
  a.sendData('new-0'); // 新连接的第一个数据，seq 重新从 0 开始
  net.run();

  console.log('-- 旧连接残包 old-1/old-2 此刻才到达 B --');
  net.releaseWhere((m) => m.kind === 'DATA');
  net.run();
  showDelivered(b);
  showStats(b);
  console.log('结论：旧残包 gen=1 与已协商对端代次 gen=2 不符，全部拒收；');
  console.log('      尽管它们的序号落在新连接的期望序号上。');
}

// ---------------------------------------------------------------------------
banner('场景 1 对照：不校验代次的朴素实现会把旧残包当新数据');
{
  const net = new Network();
  const a = new Endpoint('A', net);
  const b = new Endpoint('B', net, { checkGeneration: false });
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
  showDelivered(b);
  console.log('结论：朴素模式下旧包 old-1 序号恰好等于新连接期望序号，被误交付 —— 数据损坏。');
}

// ---------------------------------------------------------------------------
banner('场景 2：确认丢失');
{
  console.log('-- 2a. RESET_ACK 丢失：发起方超时重传 RESET，响应方幂等重答 --');
  const net = new Network();
  const a = new Endpoint('A', net);
  const b = new Endpoint('B', net);
  let drops = 1;
  net.shouldDrop = (m) => m.kind === 'RESET_ACK' && drops-- > 0;
  a.connect(b);
  net.run();
  net.shouldDrop = null;
  console.log(`  A=${a.state} B=${b.state}（A 重传 ${a.stats.retransmits} 次）`);

  console.log('-- 2b. CONFIRM 丢失：首个新代次数据隐式完成确认 --');
  const net2 = new Network();
  const a2 = new Endpoint('A', net2);
  const b2 = new Endpoint('B', net2);
  a2.connect(b2);
  net2.step();
  net2.step();
  net2.dropWhere((m) => m.kind === 'CONFIRM');
  a2.sendData('first-data');
  net2.run();
  console.log(`  A=${a2.state} B=${b2.state}（B 隐式确认 ${b2.stats.implicitConfirms} 次）`);
  showDelivered(b2);

  console.log('-- 2c. CONFIRM 丢失且无数据：响应方重传 RESET_ACK，发起方重发 CONFIRM --');
  const net3 = new Network();
  const a3 = new Endpoint('A', net3);
  const b3 = new Endpoint('B', net3);
  a3.connect(b3);
  net3.step();
  net3.step();
  net3.dropWhere((m) => m.kind === 'CONFIRM');
  net3.run();
  console.log(`  A=${a3.state} B=${b3.state}（B 重传 ${b3.stats.retransmits} 次）`);
}

// ---------------------------------------------------------------------------
banner('场景 3：双方同时重启 —— 交叉握手收敛');
{
  const net = new Network();
  const a = new Endpoint('A', net);
  const b = new Endpoint('B', net);
  a.connect(b);
  net.run();
  console.log('双方同时崩溃重启，两个 RESET 同时在途：');
  a.restart();
  b.restart();
  net.run();
  console.log(`  A=${a.state}(gen=${a.disk.gen}, 对端=${a.negotiatedPeerGen})`);
  console.log(`  B=${b.state}(gen=${b.disk.gen}, 对端=${b.negotiatedPeerGen})`);
  a.sendData('a-to-b');
  b.sendData('b-to-a');
  net.run();
  showDelivered(a);
  showDelivered(b);
  console.log('结论：双方各自接受对方的新代次并互发 RESET_ACK/CONFIRM，');
  console.log('      收敛到同一代次对 (2, 2)，无需额外仲裁。');
}

// ---------------------------------------------------------------------------
banner('场景 4：旧重置消息迟到 —— 不破坏新连接');
{
  const net = new Network();
  const a = new Endpoint('A', net);
  const b = new Endpoint('B', net);
  let holdResets = 1;
  let holdAcks = 1;
  net.shouldHold = (m) => {
    if (m.kind === 'RESET' && holdResets-- > 0) return true;
    if (m.kind === 'RESET_ACK' && holdAcks-- > 0) return true;
    return false;
  };
  a.connect(b);
  net.run(); // 第一个 RESET / RESET_ACK 被扣留，握手靠重传完成
  net.shouldHold = null;
  console.log(`gen=1 握手完成（途中各扣留了 1 个 RESET 和 RESET_ACK）`);

  a.restart();
  net.run();
  console.log(`A 重启进入 gen=2，新握手完成`);

  console.log('-- 旧 gen=1 的 RESET / RESET_ACK 此刻才到达 --');
  net.releaseWhere(() => true);
  net.run();
  showStats(a);
  showStats(b);
  console.log(`  A=${a.state} B=${b.state}（连接不受影响）`);
  a.sendData('still-works');
  net.run();
  showDelivered(b);
}

// ---------------------------------------------------------------------------
banner('场景 5：握手期间数据缓冲有上限');
{
  const net = new Network();
  const a = new Endpoint('A', net);
  const b = new Endpoint('B', net, { recvBufferCap: 2, retransmitMs: 1000, maxRetries: 10000 });
  net.shouldHold = (m) => m.kind === 'CONFIRM'; // B 停留在握手期
  a.connect(b);
  net.step();
  net.step();
  a.sendData('d0');
  a.sendData('d1');
  a.sendData('d2');
  a.sendData('d3');
  net.holdWhere((m) => m.kind === 'DATA' && m.seq === 0);
  net.run();
  console.log(`缓冲上限=2：d1/d2 入缓冲，d3 溢出丢弃（bufferDrops=${b.stats.bufferDrops}）`);
  console.log(`握手未完成，B 已交付: [${b.delivered.map((d) => d.payload).join(', ')}]（必须为空）`);
  net.shouldHold = null;
  net.releaseWhere((m) => m.kind === 'CONFIRM');
  net.step();
  net.releaseWhere((m) => m.kind === 'DATA');
  net.run();
  showDelivered(b);
  console.log('结论：握手期间数据只进有界缓冲，溢出即丢；握手完成后按代次过滤、按序交付。');
}

// ---------------------------------------------------------------------------
banner('结论：何时可以安全交付第一条数据');
console.log(`接收方交付第一条数据（seq=0）的充要条件，三者同时满足：
  1. 本端状态机已 ESTABLISHED —— 三次握手完成：
     * 发起方：收到与本地 nonce 匹配的 RESET_ACK（对方确认了本方代次）；
     * 响应方：收到 CONFIRM，或收到对端新代次的 seq=0 数据（隐式确认）。
  2. DATA.gen === 已协商的对端代次 peerGen —— 代次号持久且单调递增，
     任何旧连接残包的 gen 必然小于当前 peerGen，必然失配被拒。
  3. seq === 本代次期望序号（首包为 0）—— 序号空间随代次重新计数。
握手完成前到达的数据只能进入有界缓冲（溢出即丢弃），握手完成后按
代次过滤、按序号顺序交付；任何情况下不提前交付、不无界堆积。`);
