import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConnState } from '../src/types.ts';
import { makePair, send, waitEstablished } from './helpers.ts';

test('握手期间应用数据缓冲有上限，建立后按序冲刷', () => {
  const p = makePair({ maxBufferedBytes: 8 });
  p.a.boot(1);
  p.b.boot(1);
  for (const pkt of p.a.connect('B', p.sim.now)) p.sim.emit(pkt);

  assert.equal(send(p, p.a, 'abcd'), true);
  assert.equal(send(p, p.a, 'efgh'), true);
  assert.equal(send(p, p.a, 'ij'), false, 'buffer is capped at 8 bytes');

  waitEstablished(p);
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['abcd', 'efgh'], 'buffered data flushed in order');

  assert.equal(send(p, p.a, 'ij'), true);
  p.sim.settle();
  assert.deepEqual(p.b.delivered, ['abcd', 'efgh', 'ij']);
});

test('响应方在握手期间同样缓冲，收到第三条握手报文后冲刷', () => {
  const p = makePair();
  p.a.boot(1);
  p.b.boot(1);
  for (const pkt of p.a.connect('B', p.sim.now)) p.sim.emit(pkt);

  p.sim.runUntil(() => p.b.state === ConnState.SynReceived);
  assert.equal(send(p, p.b, 'early-reply'), true);
  assert.deepEqual(p.a.delivered, []);

  waitEstablished(p);
  p.sim.settle();
  assert.deepEqual(p.a.delivered, ['early-reply']);
});
