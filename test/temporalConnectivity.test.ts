import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  checkConnectivity,
  computeSlices,
  type DisconnectReport,
  type TemporalEdge,
} from '../src/temporalConnectivity.ts';

const edge = (u: string, v: string, start: number, end: number): TemporalEdge => ({
  u,
  v,
  start,
  end,
});

const asDisconnect = (r: ReturnType<typeof checkConnectivity>): DisconnectReport => {
  assert.equal(r.connected, false, 'expected a disconnect report');
  return r as DisconnectReport;
};

describe('checkConnectivity', () => {
  it('reports connected when edges cover the whole query interval', () => {
    const r = checkConnectivity([edge('a', 'b', 0, 10)], 'a', 'b', 2, 8);
    assert.deepEqual(r, { connected: true, slicesChecked: 1 });
  });

  it('detects a gap in the middle that endpoint-only sampling would miss', () => {
    // Endpoints 0 and 10 are covered, but [4, 6) is not.
    const edges = [edge('a', 'b', 0, 4), edge('a', 'b', 6, 12)];
    const r = asDisconnect(checkConnectivity(edges, 'a', 'b', 0, 10));
    assert.equal(r.sliceStart, 4);
    assert.equal(r.sliceEnd, 6);
    assert.deepEqual(r.reachable, ['a']);
    assert.deepEqual(
      r.cut.map((c) => ({ ...c.edge, reason: c.reason })),
      [
        { u: 'a', v: 'b', start: 0, end: 4, reason: 'expired' },
        { u: 'a', v: 'b', start: 6, end: 12, reason: 'not-yet-active' },
      ],
    );
  });

  it('treats adjacent half-open segments as seamlessly connected', () => {
    // [0,5) and [5,10) tile the query interval without a gap at t=5.
    const edges = [edge('a', 'b', 0, 5), edge('a', 'b', 5, 10)];
    const r = checkConnectivity(edges, 'a', 'b', 0, 10);
    assert.deepEqual(r, { connected: true, slicesChecked: 2 });
  });

  it('handles simultaneous close of one path and open of another at the same instant', () => {
    // Path a-x-b alive on [0,5); path a-y-b alive on [5,10). Half-open
    // semantics: at t=5 the first path is gone, the second is up.
    const edges = [
      edge('a', 'x', 0, 5),
      edge('x', 'b', 0, 5),
      edge('a', 'y', 5, 10),
      edge('y', 'b', 5, 10),
    ];
    const r = checkConnectivity(edges, 'a', 'b', 0, 10);
    assert.deepEqual(r, { connected: true, slicesChecked: 2 });
  });

  it('detects the break when a closing edge is not replaced at the same instant', () => {
    // Direct edge dies at 5; backup path only opens at 6.
    const edges = [
      edge('a', 'b', 0, 5),
      edge('a', 'y', 6, 10),
      edge('y', 'b', 6, 10),
    ];
    const r = asDisconnect(checkConnectivity(edges, 'a', 'b', 0, 10));
    assert.equal(r.sliceStart, 5);
    assert.equal(r.sliceEnd, 6);
    assert.deepEqual(r.reachable, ['a']);
    const reasons = Object.fromEntries(r.cut.map((c) => [`${c.edge.u}-${c.edge.v}`, c.reason]));
    assert.deepEqual(reasons, { 'a-b': 'expired', 'a-y': 'not-yet-active' });
  });

  it('returns the FIRST failing slice when there are several gaps', () => {
    const edges = [edge('a', 'b', 0, 2), edge('a', 'b', 3, 4), edge('a', 'b', 6, 8)];
    const r = asDisconnect(checkConnectivity(edges, 'a', 'b', 0, 8));
    assert.equal(r.sliceStart, 2);
    assert.equal(r.sliceEnd, 3);
  });

  it('clips edge windows to the query interval and ignores outside events', () => {
    // Edge events at -100 and 100 must not create slices outside [3, 7).
    const edges = [edge('a', 'b', -100, 3), edge('a', 'b', 7, 100)];
    const r = asDisconnect(checkConnectivity(edges, 'a', 'b', 3, 7));
    assert.equal(r.sliceStart, 3);
    assert.equal(r.sliceEnd, 7);
    assert.deepEqual(computeSlices(edges, 3, 7), [{ start: 3, end: 7 }]);
  });

  it('is vacuously connected on an empty query interval', () => {
    const r = checkConnectivity([], 'a', 'b', 5, 5);
    assert.deepEqual(r, { connected: true, slicesChecked: 0 });
  });

  it('throws on an inverted query interval', () => {
    assert.throws(() => checkConnectivity([], 'a', 'b', 6, 5), RangeError);
  });

  it('treats source === target as trivially connected', () => {
    const r = checkConnectivity([], 'a', 'a', 0, 10);
    assert.deepEqual(r, { connected: true, slicesChecked: 0 });
  });

  it('ignores degenerate edges with start >= end', () => {
    const edges = [edge('a', 'b', 5, 5), edge('a', 'b', 7, 3)];
    const r = asDisconnect(checkConnectivity(edges, 'a', 'b', 0, 10));
    assert.equal(r.sliceStart, 0);
    assert.equal(r.sliceEnd, 10);
    assert.deepEqual(r.cut, []);
  });

  it('reports the full reachable component and multi-edge cut as evidence', () => {
    // During [4, 6): a-c and c-b both down; reachable from a is {a, d}.
    const edges = [
      edge('a', 'd', 0, 10),
      edge('a', 'c', 0, 4),
      edge('c', 'b', 0, 4),
      edge('a', 'c', 6, 10),
      edge('c', 'b', 6, 10),
      edge('x', 'y', 0, 1), // unrelated, expired before the failing slice
    ];
    const r = asDisconnect(checkConnectivity(edges, 'a', 'b', 0, 10));
    assert.equal(r.sliceStart, 4);
    assert.equal(r.sliceEnd, 6);
    assert.deepEqual(r.reachable, ['a', 'd']);
    const cutKeys = r.cut.map((c) => `${c.edge.u}-${c.edge.v}:${c.reason}`).sort();
    assert.deepEqual(cutKeys, ['a-c:expired', 'a-c:not-yet-active']);
  });

  it('handles multi-hop paths and parallel edges across slices', () => {
    const edges = [
      edge('a', 'b', 0, 10),
      edge('b', 'c', 0, 5),
      edge('b', 'c', 5, 10), // adjacent renewal
      edge('c', 'd', 2, 8),
    ];
    const r = checkConnectivity(edges, 'a', 'd', 0, 10);
    assert.equal(r.connected, false);
    const d = r as DisconnectReport;
    assert.equal(d.sliceStart, 0);
    assert.equal(d.sliceEnd, 2);
  });

  it('disconnects from the very first slice when never connected', () => {
    const r = asDisconnect(checkConnectivity([edge('a', 'b', 0, 10)], 'a', 'z', 0, 10));
    assert.equal(r.sliceStart, 0);
    assert.equal(r.sliceEnd, 10);
    assert.deepEqual(r.reachable, ['a', 'b']);
    assert.deepEqual(r.cut, []);
  });
});

describe('computeSlices', () => {
  it('slices exactly at effective endpoints within the query range', () => {
    const edges = [edge('a', 'b', 1, 4), edge('b', 'c', 3, 8)];
    assert.deepEqual(computeSlices(edges, 0, 10), [
      { start: 0, end: 1 },
      { start: 1, end: 3 },
      { start: 3, end: 4 },
      { start: 4, end: 8 },
      { start: 8, end: 10 },
    ]);
  });

  it('produces a single slice when no edge events fall inside', () => {
    assert.deepEqual(computeSlices([edge('a', 'b', 0, 10)], 2, 8), [{ start: 2, end: 8 }]);
  });
});
