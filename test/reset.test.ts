import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConnState, PacketKind, type Generation, type Packet } from '../src/types.ts';
import { bootAndConnect, makePair, send, waitEstablished } from './helpers.ts';

test('对端重启：RESET 回显当前代次对才被接受，随后自动重新握手', () => {
  const p = makePair();
  bootAndConnect(p);
  waitEstablished(p);

  const genA1 = p.a.myGenObj as Generation;
  const genB1 = p.b.myGenObj as Generation;

  p.b.boot(2);
  send(p, p.a, 'to-old-b');
  waitEstablished(p);

  const reset = p.sim.sentLog.find((x) => x.kind === PacketKind.Reset && x.src === 'B') as Packet;
  assert.ok(reset, 'restarted B rejects old-generation DATA with RESET');
  assert.equal(reset.echoSrcGen, genA1);
  assert.equal(reset.echoDstGen, genB1);

  assert.ok(p.a.log.some((l) => l.includes('RESET accepted')));
  assert.ok(p.a.log.some((l) => l.includes('reconnect after RESET')));
  assert.equal(p.b.peerGeneration, p.a.generation);

  send(p, p.a, 'fresh');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['fresh']);
});

test('旧 RESET 迟到不会拆散新连接', () => {
  const p = makePair();
  bootAndConnect(p);
  waitEstablished(p);

  p.b.boot(2);
  send(p, p.a, 'to-old-b');
  waitEstablished(p);

  const oldReset = p.sim.sentLog.find((x) => x.kind === PacketKind.Reset && x.src === 'B') as Packet;
  assert.ok(oldReset);

  p.sim.inject(oldReset, p.sim.now + 1);
  p.sim.settle();

  assert.equal(p.a.state, ConnState.Established, 'delayed old RESET must be ignored');
  assert.ok(p.a.log.some((l) => l.includes('ignore stale RESET')));

  send(p, p.a, 'still-alive');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['still-alive']);
});

test('双方同时重启：交叉 SYN 建立唯一新连接，旧残包被拒', () => {
  const p = makePair();
  bootAndConnect(p);
  waitEstablished(p);

  const genA1 = p.a.myGenObj as Generation;
  const genB1 = p.b.myGenObj as Generation;

  p.a.boot(2);
  p.b.boot(2);
  for (const pkt of p.a.connect('B', p.sim.now)) p.sim.emit(pkt);
  for (const pkt of p.b.connect('A', p.sim.now)) p.sim.emit(pkt);
  waitEstablished(p);

  assert.equal(p.a.peerGeneration, p.b.generation);
  assert.equal(p.b.peerGeneration, p.a.generation);

  send(p, p.a, 'ping');
  send(p, p.b, 'pong');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['ping']);
  assert.deepEqual(p.a.delivered, ['pong']);

  const staleAB: Packet = {
    kind: PacketKind.Data,
    src: 'A',
    dst: 'B',
    srcGen: genA1,
    dstGen: genB1,
    seq: p.b.rcvNext,
    ack: 0,
    payload: 'ghost-a',
  };
  const staleBA: Packet = {
    kind: PacketKind.Data,
    src: 'B',
    dst: 'A',
    srcGen: genB1,
    dstGen: genA1,
    seq: p.a.rcvNext,
    ack: 0,
    payload: 'ghost-b',
  };
  p.sim.inject(staleAB, p.sim.now + 1);
  p.sim.inject(staleBA, p.sim.now + 2);
  p.sim.settle();

  assert.deepEqual(p.b.delivered, ['ping']);
  assert.deepEqual(p.a.delivered, ['pong']);
  assert.equal(p.a.state, ConnState.Established);
  assert.equal(p.b.state, ConnState.Established);
});

test('迟到的旧 SYN 残包无法建立幽灵连接', () => {
  const p = makePair();
  bootAndConnect(p);
  waitEstablished(p);

  const genA1 = p.a.myGenObj as Generation;

  p.a.boot(2);
  for (const pkt of p.a.connect('B', p.sim.now)) p.sim.emit(pkt);
  waitEstablished(p);

  const staleSyn: Packet = {
    kind: PacketKind.Syn,
    src: 'A',
    dst: 'B',
    srcGen: genA1,
    dstGen: null,
    seq: 1,
    ack: -1,
    payload: '',
  };
  p.sim.inject(staleSyn, p.sim.now + 1);
  p.sim.settle();

  assert.equal(p.b.state, ConnState.Established, 'stale SYN must not tear down the connection');
  assert.equal(p.b.peerGeneration, p.a.generation);

  send(p, p.a, 'alive');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['alive']);
});
