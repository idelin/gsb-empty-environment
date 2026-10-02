import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Disseminator, retransmitLimit } from '../src/disseminator.ts';

describe('retransmitLimit', () => {
  it('scales as lambda * log2(N)', () => {
    assert.equal(retransmitLimit(100), Math.ceil(3 * Math.log2(101)));
    assert.equal(retransmitLimit(1), 3);
    assert.equal(retransmitLimit(0), 1);
  });
});

describe('Disseminator', () => {
  it('piggybacks queued events and counts propagations', () => {
    const d = new Disseminator(100);
    d.add({ type: 'join', nodeId: 'n1', incarnation: 0 });
    const first = d.getPiggyback(6);
    assert.equal(first.length, 1);
    assert.equal(first[0].nodeId, 'n1');
  });

  it('prefers least-propagated events first', () => {
    const d = new Disseminator(100);
    d.add({ type: 'join', nodeId: 'a', incarnation: 0 });
    d.add({ type: 'join', nodeId: 'b', incarnation: 0 });
    d.getPiggyback(2);
    d.add({ type: 'join', nodeId: 'c', incarnation: 0 });
    const next = d.getPiggyback(1);
    assert.equal(next[0].nodeId, 'c');
  });

  it('bounds piggyback size per packet', () => {
    const d = new Disseminator(0);
    for (let i = 0; i < 10; i++) {
      d.add({ type: 'join', nodeId: `n${i}`, incarnation: 0 });
    }
    assert.equal(d.getPiggyback(6).length, 6);
    assert.equal(d.getPiggyback(6).length, 4);
  });

  it('drops events after the max retransmit rounds', () => {
    const d = new Disseminator(4);
    d.add({ type: 'suspect', nodeId: 'x', incarnation: 1 });
    const limit = d.maxRounds;
    for (let i = 0; i < limit; i++) {
      assert.equal(d.getPiggyback(6).length, 1, `round ${i}`);
    }
    assert.equal(d.getPiggyback(6).length, 0);
    assert.equal(d.pending, 0);
  });

  it('dedupes by event key and keeps the newest incarnation', () => {
    const d = new Disseminator(100);
    d.add({ type: 'alive', nodeId: 'n1', incarnation: 1 });
    d.add({ type: 'alive', nodeId: 'n1', incarnation: 2 });
    d.add({ type: 'alive', nodeId: 'n1', incarnation: 1 });
    assert.equal(d.pending, 1);
    assert.equal(d.getPiggyback(6)[0].incarnation, 2);
  });
});
