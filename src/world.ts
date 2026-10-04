// World: holds bodies, applies updates in batches, and answers CCD queries.
//
// Batch semantics: upsert/remove calls are queued and only take effect at
// commit(). Queries always see the last committed state.
//
// Stable IDs: once an id is removed it is retired forever; re-adding a
// retired id throws. The broad-phase grid is rebuilt from live bodies at
// every query, so a deleted body can never resurrect from a stale cell.

import { Fraction, F } from './fraction.js';
import type { Body, ContactInterval } from './ccd.js';
import { broadPhase, allPairsContacts, pairContactInterval, defaultCellSize } from './ccd.js';

export interface BodyInput {
  id: string;
  x: Fraction | bigint | number | string;
  y: Fraction | bigint | number | string;
  w: Fraction | bigint | number | string;
  h: Fraction | bigint | number | string;
  vx: Fraction | bigint | number | string;
  vy: Fraction | bigint | number | string;
}

interface PendingOp {
  kind: string;           // 'upsert' or 'remove'
  id: string;
  body: Body | undefined;
}

export interface QueryResult {
  T: Fraction;
  bodies: number;
  candidatePairs: number;      // pairs produced by broad phase
  collisions: ContactInterval[];   // exact contact intervals (from candidates)
  exactPairs: number;         // colliding pairs found by independent all-pairs check
  verified: boolean;          // candidate-derived result == all-pairs result
  missedPairs: string[];        // pairs the broad phase missed (must be empty)
}

export class World {
  private live: Map<string, Body> = new Map();
  private pending: PendingOp[] = [];
  private retired: Set<string> = new Set();

  upsert (input: BodyInput) {
    this.pending.push({ kind: 'upsert', id: input.id, body: normalizeBody(input) });
  }

  remove (id: string) {
    this.pending.push({ kind: 'remove', id, body: undefined });
  }

  commit () {
    for (const op of this.pending) {
      if (op.kind === 'upsert') {
        if (this.retired.has(op.id)) {
          throw new Error('id "' + op.id + '" was deleted and cannot be resurrected');
        }
        this.live.set(op.id, op.body);
      } else {
        if (this.live.has(op.id)) {
          this.live.delete(op.id);
          this.retired.add(op.id);
        }
      }
    }
    this.pending = [];
  }

  bodyCount () {
    return this.live.size;
  }

  query (T: FractionInput, cellSize: Fraction | undefined) {
    const timeHorizon = F(T);
    const bodies = [...this.live.values()];
    let cs = defaultCellSize(bodies, timeHorizon);
    if (cellSize !== undefined) cs = F(cellSize);
    const broad = broadPhase(bodies, timeHorizon, cs);
    const collisions: ContactInterval[] = [];
    for (const pair of broad.pairs) {
      const interval = pairContactInterval(pair[0], pair[1], timeHorizon);
      if (interval !== null) {
        collisions.push({ a: pair[0].id, b: pair[1].id, start: interval[0], end: interval[1] });
      }
    }
    // independent all-pairs exact verification
    const exact = allPairsContacts(bodies, timeHorizon);
    const exactKeys = new Set(exact.map(c => pairKey(c.a, c.b)));
    const candKeys = new Set(collisions.map(c => pairKey(c.a, c.b)));
    const missed = [...exactKeys].filter(k => !candKeys.has(k));
    const spurious = [...candKeys].filter(k => !exactKeys.has(k));
    // interval-level equality check
    let verified = missed.length === 0 && spurious.length === 0;
    if (verified) {
      const byKey = new Map(exact.map(c => [pairKey(c.a, c.b), c]));
      for (const c of collisions) {
        const e = byKey.get(pairKey(c.a, c.b));
        if (e === undefined || c.start.equals(e.start) === false || c.end.equals(e.end) === false) {
          verified = false;
          break;
        }
      }
    }
    collisions.sort((a, b) => pairKey(a.a, a.b).localeCompare(pairKey(b.a, b.b)));
    return {
      T: timeHorizon,
      bodies: bodies.length,
      candidatePairs: broad.pairs.length,
      collisions,
      exactPairs: exact.length,
      verified,
      missedPairs: missed,
    };
  }
}

function pairKey (a: string, b: string) {
  if (a < b) return a + '|' + b;
  return b + '|' + a;
}

function normalizeBody (input: BodyInput) {
  const w = F(input.w);
  const h = F(input.h);
  if (w.sign() < 0 || h.sign() < 0) throw new Error('width/height must be >= 0');
  return {
    id: input.id,
    x: F(input.x), y: F(input.y),
    w, h,
    vx: F(input.vx), vy: F(input.vy),
  };
}
