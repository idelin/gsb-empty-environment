/**
 * Randomised validation against the per-character identity model.
 *
 * Every structural mapping produced by the history (single edges, composed
 * ranges, inverses, and queries after compression) is checked against the
 * ground truth derived from character identities. Selections are carried
 * along randomly generated edits — including merges, undo branches and
 * history reclamation — and invariants are asserted at each step.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import { textOf, type Edit } from '../src/model.ts';
import { mapBoundary } from '../src/mapping.ts';
import {
  directionOf,
  mapSelectionBack,
  mapSelectionsForward,
  selectionToUtf16,
  utf16ToBoundary,
  type Selection,
  type Stickiness,
} from '../src/selection.ts';
import { groundTruthForward, mulberry32, pick, randInt } from './helpers.ts';

const ALPHABET = ['a', 'b', 'c', 'x', 'é', '😀', '🎉', '\n', '\r\n', ' ', 'de'];

function randomText(rng: () => number, maxLen: number): string {
  const n = randInt(rng, 0, maxLen);
  let out = '';
  for (let i = 0; i < n; i++) out += pick(rng, ALPHABET);
  return out;
}

function randomEdit(rng: () => number, docLength: number): Edit {
  const start = randInt(rng, 0, docLength);
  const deletable = Math.min(6, docLength - start);
  if (rng() < 0.45 && deletable > 0) {
    const deleteCount = randInt(rng, 1, deletable);
    return { start, deleteCount, insert: rng() < 0.3 ? randomText(rng, 3) : '' };
  }
  return { start, deleteCount: 0, insert: randomText(rng, 5) };
}

function randomSelection(rng: () => number, id: number, docLength: number): Selection {
  const stick = (): Stickiness => (rng() < 0.5 ? 'left' : 'right');
  const a = randInt(rng, 0, docLength);
  const b = randInt(rng, 0, docLength);
  return {
    id,
    anchor: { boundary: a, stick: stick() },
    active: { boundary: b, stick: stick() },
  };
}

function checkSelectionsValid(h: History, selections: Selection[]): void {
  const version = h.getVersion(h.head);
  const n = version.chars.length;
  for (const sel of selections) {
    for (const ep of [sel.anchor, sel.active]) {
      assert.ok(ep.boundary >= 0 && ep.boundary <= n, 'boundary in range');
    }
    // public UTF-16 offsets must round-trip without splitting surrogate pairs
    const utf16 = selectionToUtf16(version, sel);
    assert.equal(utf16ToBoundary(version, utf16.anchor), sel.anchor.boundary);
    assert.equal(utf16ToBoundary(version, utf16.active), sel.active.boundary);
    assert.ok(
      ['forward', 'backward', 'collapsed'].includes(directionOf(sel)),
      'direction derivable',
    );
  }
}

function checkMappingAgainstGroundTruth(h: History, from: number, to: number): void {
  const m = h.mappingBetween(from, to);
  assert.deepEqual(
    m.forward,
    groundTruthForward(h.getVersion(from), h.getVersion(to)),
    `mapping ${from}->${to} matches identity ground truth`,
  );
}

test('fuzz: random edits, selection merges, undo branches and reclamation', () => {
  const rng = mulberry32(0x5eed);
  const h = new History(randomText(rng, 12));
  let shadow = textOf(h.getVersion(h.head));
  let selections: Selection[] = [];
  let nextSelId = 1;
  let mergeEvents = 0;
  let undoEvents = 0;
  let compressions = 0;

  const respawn = () => {
    const n = h.getVersion(h.head).chars.length;
    if (rng() < 0.5 && n > 0) {
      // clustered selections: likely to be pressed together by one deletion
      const center = randInt(rng, 0, n);
      const near = () => Math.max(0, Math.min(n, center + randInt(rng, -2, 2)));
      selections = Array.from({ length: randInt(rng, 2, 4) }, () => ({
        id: nextSelId++,
        anchor: { boundary: near(), stick: pick(rng, ['left', 'right'] as const) },
        active: { boundary: near(), stick: pick(rng, ['left', 'right'] as const) },
      }));
    } else {
      selections = Array.from({ length: randInt(rng, 1, 4) }, () =>
        randomSelection(rng, nextSelId++, n),
      );
    }
  };
  respawn();

  for (let round = 0; round < 400; round++) {
    const action = rng();

    if (action < 0.62) {
      // random edit at head
      const before = h.head;
      const edit = randomEdit(rng, h.getVersion(before).chars.length);
      let after: number;
      try {
        after = h.addVersion(edit);
      } catch (err) {
        // branching over a pinned redo tail is refused by design; unpin and retry
        assert.match(String(err), /cannot branch/);
        for (const id of h.liveVersionIds) h.unpin(id);
        after = h.addVersion(edit);
      }

      // shadow model: plain string splice in code points
      const cps = Array.from(shadow);
      cps.splice(edit.start, edit.deleteCount, ...Array.from(edit.insert ?? ''));
      shadow = cps.join('');
      assert.equal(textOf(h.getVersion(after)), shadow, 'model matches string splice');

      // edge mapping matches identity ground truth
      checkMappingAgainstGroundTruth(h, before, after);

      // carry selections across the edit; merges are recorded
      const edge = h.mappingBetween(before, after);
      const fwd = mapSelectionsForward(selections, edge);
      mergeEvents += fwd.merges.length;
      for (const merge of fwd.merges) {
        const kept = fwd.selections.find((s) => s.id === merge.keptId);
        assert.ok(kept?.mergedFrom, 'merge keeps provenance');
        assert.equal(kept.mergedFrom.sources.length, merge.absorbedIds.length + 1);
        assert.equal(kept.mergedFrom.sourceVersion, before);
        for (const src of kept.mergedFrom.sources) {
          assert.ok(src.id >= 1 && src.direction && src.anchor.stick && src.active.stick);
        }
      }
      selections = fwd.selections;
      checkSelectionsValid(h, selections);
    } else if (action < 0.74 && h.liveVersionIds.indexOf(h.head) > 0) {
      // undo a few steps: selections travel back, merges restore originals
      const back = h.liveVersionIds.indexOf(h.head);
      const steps = randInt(rng, 1, Math.min(3, back));
      for (let s = 0; s < steps; s++) {
        const fromHead = h.head;
        const toHead = h.undo();
        undoEvents++;
        // m.from is the version being restored to; the selection lives in m.to
        const m = h.mappingBetween(toHead, fromHead);
        const restored: Selection[] = [];
        for (const sel of selections) {
          const back = mapSelectionBack(sel, m);
          restored.push(...back.selections);
          if (back.ambiguous) {
            assert.ok(back.ambiguity.length > 0, 'ambiguity source is reported');
            assert.ok(back.lostContext.length > 0);
          }
        }
        selections = restored;
        checkSelectionsValid(h, selections);
      }
      shadow = textOf(h.getVersion(h.head));
    } else if (action < 0.82) {
      // compress history: surviving-version queries must be unaffected
      const ids = h.liveVersionIds;
      for (const id of ids) if (rng() < 0.3) h.pin(id);
      const survivors = ids.filter((id) => h.hasVersion(id));
      const pairs: Array<[number, number]> = [];
      for (const a of survivors) for (const b of survivors) if (a !== b) pairs.push([a, b]);
      const snapshot = new Map<string, number[]>();
      for (const [a, b] of pairs) {
        if (!h.hasVersion(a) || !h.hasVersion(b)) continue;
        snapshot.set(`${a}->${b}`, h.mappingBetween(a, b).forward.slice());
      }
      h.compress();
      compressions++;
      for (const [key, expected] of snapshot) {
        const [a, b] = key.split('->').map(Number);
        if (!h.hasVersion(a) || !h.hasVersion(b)) continue;
        assert.deepEqual(h.mappingBetween(a, b).forward, expected, `query ${key} survives compression`);
        checkMappingAgainstGroundTruth(h, a, b);
      }
    } else if (action < 0.9) {
      respawn();
    } else {
      // spot-check a random pair of surviving versions against ground truth
      const ids = h.liveVersionIds;
      const a = pick(rng, ids);
      const b = pick(rng, ids);
      checkMappingAgainstGroundTruth(h, a, b);
      // Boundary mapping is associative for positions anchored on characters
      // that survive the whole way: stepping edge by edge equals the composed
      // mapping. (Arbitrary positions may legitimately differ, because a
      // composed mapping treats characters inserted en route as stick targets.)
      const m = h.mappingBetween(a, b);
      const ia = ids.indexOf(a);
      const ib = ids.indexOf(b);
      const stepBoundary = (probe: number, stick: Stickiness): number => {
        let stepped = probe;
        if (ia <= ib) {
          for (let k = ia; k < ib; k++) {
            stepped = mapBoundary(h.mappingBetween(ids[k], ids[k + 1]), stepped, stick);
          }
        } else {
          for (let k = ia; k > ib; k--) {
            stepped = mapBoundary(h.mappingBetween(ids[k], ids[k - 1]), stepped, stick);
          }
        }
        return stepped;
      };
      const survivors: number[] = [];
      for (let i = 0; i < m.forward.length; i++) if (m.forward[i] >= 0) survivors.push(i);
      for (let t = 0; t < 8 && survivors.length > 0; t++) {
        const i = pick(rng, survivors);
        assert.equal(
          stepBoundary(i, 'right'),
          mapBoundary(m, i, 'right'),
          `survivor boundary ${i} (right) ${a}->${b}`,
        );
        assert.equal(
          stepBoundary(i + 1, 'left'),
          mapBoundary(m, i + 1, 'left'),
          `survivor boundary ${i + 1} (left) ${a}->${b}`,
        );
      }
    }
  }

  assert.ok(mergeEvents > 0, 'fuzz exercised selection merges');
  assert.ok(undoEvents > 0, 'fuzz exercised undo');
  assert.ok(compressions > 0, 'fuzz exercised history reclamation');
  console.log(
    `  fuzz stats: merges=${mergeEvents} undos=${undoEvents} compressions=${compressions} versions=${h.liveVersionIds.length}`,
  );
});
