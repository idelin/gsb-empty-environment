import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConnState, PacketKind, type Generation, type Packet } from '../src/types.ts';
import { bootAndConnect, makePair, send, waitEstablished } from './helpers.ts';

test('三次握手：双方确认新代次后才交付第一条数据', () => {
  const p = makePair();
  bootAndConnect(p);
  waitEstablished(p);

  assert.equal(p.a.state, ConnState.Established);
  assert.equal(p.b.state, ConnState.Established);
  assert.equal(p.a.peerGeneration, p.b.generation);
  assert.equal(p.b.peerGeneration, p.a.generation);
  assert.deepEqual(p.b.delivered, []);

  send(p, p.a, 'hello');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['hello']);

  const establishedIdx = p.b.log.findIndex((l) => l.includes('established'));
  const deliverIdx = p.b.log.findIndex((l) => l.includes('deliver'));
  assert.ok(establishedIdx !== -1, 'responder must log established');
  assert.ok(deliverIdx > establishedIdx, 'first delivery only after handshake completes');

  const syn = p.sim.sentLog.find((pkt) => pkt.kind === PacketKind.Syn) as Packet;
  const firstData = p.sim.sentLog.find((pkt) => pkt.kind === PacketKind.Data) as Packet;
  assert.equal(firstData.seq, syn.seq + 1, 'first data seq = ISN + 1 (SYN consumes one)');
});

test('旧连接残包（旧代次、序号完全重叠）不会被当作新数据交付', () => {
  const p = makePair();
  bootAndConnect(p);
  waitEstablished(p);
  send(p, p.a, 'old-data');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['old-data']);

  const genA1 = p.a.myGenObj as Generation;
  const genB1 = p.b.myGenObj as Generation;

  p.a.boot(2);
  for (const pkt of p.a.connect('B', p.sim.now)) p.sim.emit(pkt);
  waitEstablished(p);

  const stale: Packet = {
    kind: PacketKind.Data,
    src: 'A',
    dst: 'B',
    srcGen: genA1,
    dstGen: genB1,
    seq: p.b.rcvNext,
    ack: 0,
    payload: 'stale-garbage',
  };
  p.sim.inject(stale, p.sim.now + 1);
  p.sim.settle();

  assert.deepEqual(p.b.delivered, ['old-data'], 'stale packet must not be delivered');
  const reset = p.sim.sentLog.find((x) => x.kind === PacketKind.Reset && x.src === 'B');
  assert.ok(reset, 'B answers stale packet with RESET');
  assert.equal(reset.echoSrcGen, genA1);
  assert.equal(reset.echoDstGen, genB1);
});

test('握手 ACK 丢失：SYN-ACK 重传完成握手，数据不重复交付', () => {
  let dropped = false;
  const p = makePair({
    hooks: {
      drop: (pkt) => {
        if (!dropped && pkt.kind === PacketKind.Ack && pkt.src === 'A') {
          dropped = true;
          return true;
        }
        return false;
      },
    },
  });
  bootAndConnect(p);
  waitEstablished(p);

  const synAcks = p.sim.sentLog.filter((x) => x.kind === PacketKind.SynAck);
  assert.ok(synAcks.length >= 2, 'responder retransmits SYN-ACK after ACK loss');

  send(p, p.a, 'hello');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['hello']);
});

test('数据包丢失：超时重传，恰好交付一次', () => {
  let dropped = false;
  const p = makePair({
    hooks: {
      drop: (pkt) => {
        if (!dropped && pkt.kind === PacketKind.Data && pkt.src === 'A') {
          dropped = true;
          return true;
        }
        return false;
      },
    },
  });
  bootAndConnect(p);
  waitEstablished(p);
  send(p, p.a, 'reliable');
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['reliable']);
});
