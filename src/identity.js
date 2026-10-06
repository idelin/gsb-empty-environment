/**
 * Explicit statement identity model.
 *
 * Every statement known to a store owns a stable StatementId (string).
 * When the source changes, `matchStatements` computes how ids transfer
 * from the previous statement list to the next one, in four passes:
 *
 *  1. Survivors — statements whose span no edit touched keep their id,
 *     matched by shifted position + content key.
 *  2. Content pairing — remaining statements pair up by identical content
 *     key (kind + normalized tokens), in document order. This carries ids
 *     across moves and reformats.
 *  3. Region pairing — within each edit region, remaining prev/next
 *     statements pair in document order. This keeps identity when a
 *     statement is edited in place (its content key changed).
 *  4. Tombstones — leftover next-statements may revive recently deleted
 *     ids with the same content key (cut/paste, delete/undo). Anything
 *     still unmatched is a brand-new statement; leftover prev-statements
 *     are deleted and become tombstones themselves.
 *
 * The model never transfers an id to a statement at an unrelated location:
 * deletions unbind, they do not re-anchor.
 */

import { mapOffsetThroughEdits, rangesIntersect } from './text.js';

export class IdentityModel {
  constructor({ tombstoneLimit = 256 } = {}) {
    this.nextId = 1;
    // key -> array of { id, key } awaiting revival (FIFO per key)
    this.tombstones = new Map();
    this.tombstoneCount = 0;
    this.tombstoneLimit = tombstoneLimit;
  }

  freshId() {
    return 's' + this.nextId++;
  }

  /** Assign ids to every statement of an initial parse. */
  assignInitial(statements) {
    return statements.map((s) => ({ ...s, id: this.freshId() }));
  }

  bury(id, key) {
    if (this.tombstoneCount >= this.tombstoneLimit) {
      // Evict the oldest tombstone overall (first key's head).
      const firstKey = this.tombstones.keys().next().value;
      const bucket = this.tombstones.get(firstKey);
      bucket.shift();
      if (bucket.length === 0) this.tombstones.delete(firstKey);
      this.tombstoneCount--;
    }
    let bucket = this.tombstones.get(key);
    if (!bucket) {
      bucket = [];
      this.tombstones.set(key, bucket);
    }
    bucket.push({ id, key });
    this.tombstoneCount++;
  }

  revive(key) {
    const bucket = this.tombstones.get(key);
    if (!bucket || bucket.length === 0) return null;
    const entry = bucket.shift();
    if (bucket.length === 0) this.tombstones.delete(key);
    this.tombstoneCount--;
    return entry.id;
  }

  clearTombstones() {
    this.tombstones.clear();
    this.tombstoneCount = 0;
  }

  /**
   * Transfer ids from `prev` (statements with ids) to `next` (fresh parse)
   * across `edits` (in prev-text coordinates).
   * Returns { matched: nextStatementsWithIds, deleted: [{id, key}] }.
   */
  matchStatements(prev, next, edits) {
    const matched = next.map((s) => ({ ...s, id: null }));
    const prevUsed = new Array(prev.length).fill(false);

    // Index next statements by start offset for survivor lookup.
    const nextByStart = new Map();
    for (let i = 0; i < matched.length; i++) {
      const list = nextByStart.get(matched[i].start) || [];
      list.push(i);
      nextByStart.set(matched[i].start, list);
    }

    // Pass 1: survivors — span untouched by every edit, position shifted.
    for (let p = 0; p < prev.length; p++) {
      const s = prev[p];
      const touched = edits.some((e) => rangesIntersect(e.start, e.end, s.start, s.end));
      if (touched) continue;
      const shiftedStart = mapOffsetThroughEdits(s.start, edits);
      const candidates = nextByStart.get(shiftedStart);
      if (!candidates) continue;
      const hit = candidates.find((ni) => matched[ni].id === null && matched[ni].key === s.key);
      if (hit !== undefined) {
        matched[hit].id = s.id;
        prevUsed[p] = true;
      }
    }

    // Pass 2: content pairing among the unmatched, per key, document order.
    const unmatchedNextByKey = new Map();
    for (let i = 0; i < matched.length; i++) {
      if (matched[i].id !== null) continue;
      const list = unmatchedNextByKey.get(matched[i].key) || [];
      list.push(i);
      unmatchedNextByKey.set(matched[i].key, list);
    }
    for (let p = 0; p < prev.length; p++) {
      if (prevUsed[p]) continue;
      const list = unmatchedNextByKey.get(prev[p].key);
      if (list && list.length > 0) {
        const ni = list.shift();
        matched[ni].id = prev[p].id;
        prevUsed[p] = true;
      }
    }

    // Pass 3: region pairing — per edit region, pair remaining in doc order.
    for (const e of edits) {
      const prevInRegion = [];
      for (let p = 0; p < prev.length; p++) {
        if (!prevUsed[p] && rangesIntersect(e.start, e.end, prev[p].start, prev[p].end)) {
          prevInRegion.push(p);
        }
      }
      if (prevInRegion.length === 0) continue;
      const repStart = e.start;
      const repEnd = e.start + e.text.length;
      const nextInRegion = [];
      for (let i = 0; i < matched.length; i++) {
        if (matched[i].id === null && rangesIntersect(repStart, repEnd, matched[i].start, matched[i].end)) {
          nextInRegion.push(i);
        }
      }
      const pairs = Math.min(prevInRegion.length, nextInRegion.length);
      for (let k = 0; k < pairs; k++) {
        matched[nextInRegion[k]].id = prev[prevInRegion[k]].id;
        prevUsed[prevInRegion[k]] = true;
      }
    }

    // Pass 4: tombstone revival for still-unmatched next statements.
    for (let i = 0; i < matched.length; i++) {
      if (matched[i].id !== null) continue;
      const revived = this.revive(matched[i].key);
      if (revived !== null) matched[i].id = revived;
    }

    // Leftover prev statements are deleted: bury them as tombstones.
    const deleted = [];
    for (let p = 0; p < prev.length; p++) {
      if (prevUsed[p]) continue;
      deleted.push({ id: prev[p].id, key: prev[p].key });
      this.bury(prev[p].id, prev[p].key);
    }

    // Anything still unmatched is genuinely new.
    for (let i = 0; i < matched.length; i++) {
      if (matched[i].id === null) matched[i].id = this.freshId();
    }

    return { matched, deleted };
  }
}
