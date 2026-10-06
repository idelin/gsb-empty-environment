import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Snapper } from '../src/index.ts';

const SUB = 64; // subpixels per logical px
const px = (n: number): number => n * SUB;

function makeSnapper(zoom = 1): Snapper {
  return new Snapper({ snapThresholdPx: 4, releaseThresholdPx: 8, zoom });
}

describe('nearest-line snapping', () => {
  it('snaps to the nearest line within the threshold', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v1', axis: 'vertical', position: px(0) });
    s.addLine({ id: 'v2', axis: 'vertical', position: px(10) });
    const r = s.update(px(8), 0);
    assert.equal(r.x.snapped, true);
    assert.equal(r.x.lineId, 'v2');
    assert.equal(r.x.position, px(10));
  });

  it('leaves the coordinate untouched outside the threshold', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v1', axis: 'vertical', position: px(0) });
    const r = s.update(px(5), 0); // 5px away, threshold is 4px
    assert.deepEqual(r.x, { position: px(5), snapped: false, lineId: null });
  });

  it('breaks distance ties by ascending line id', () => {
    const s = makeSnapper();
    s.addLine({ id: 'b', axis: 'vertical', position: px(-1) });
    s.addLine({ id: 'a', axis: 'vertical', position: px(1) });
    const r = s.update(0, 0); // exactly 1px from both
    assert.equal(r.x.snapped, true);
    assert.equal(r.x.lineId, 'a');
  });

  it('breaks ties numerically for numeric ids', () => {
    const s = makeSnapper();
    s.addLine({ id: 10, axis: 'vertical', position: px(-1) });
    s.addLine({ id: 2, axis: 'vertical', position: px(1) });
    assert.equal(s.update(0, 0).x.lineId, 2);
  });
});

describe('hysteresis along a continuous trajectory', () => {
  it('snaps at the exact snap boundary and releases at the exact release boundary', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });

    // Phase A: sweep inward one subpixel at a time, snap must trigger exactly at 4px.
    let snappedAt: number | null = null;
    for (let p = px(5); p >= 0; p -= 1) {
      const r = s.update(p, 0).x;
      if (r.snapped && snappedAt === null) snappedAt = p;
      if (r.snapped) assert.equal(r.position, 0);
      else assert.equal(r.position, p);
    }
    assert.equal(snappedAt, px(4));

    // Phase B: sweep back out; the snap must hold through 8px and release one
    // subpixel past it, proving the release threshold is what governs here.
    let releasedAt: number | null = null;
    for (let p = 1; p <= px(9); p += 1) {
      const r = s.update(p, 0).x;
      if (!r.snapped && releasedAt === null) releasedAt = p;
      if (r.snapped) assert.equal(r.position, 0);
      else assert.equal(r.position, p);
    }
    assert.equal(releasedAt, px(8) + 1);

    // Phase C: sweep in again; no re-snap until the (smaller) snap threshold.
    let resnappedAt: number | null = null;
    for (let p = px(9); p >= 0; p -= 1) {
      const r = s.update(p, 0).x;
      if (r.snapped && resnappedAt === null) resnappedAt = p;
    }
    assert.equal(resnappedAt, px(4));
  });

  it('stays snapped inside the release band even when another line is nearer', () => {
    const s = makeSnapper();
    s.addLine({ id: 'a', axis: 'vertical', position: px(0) });
    s.addLine({ id: 'b', axis: 'vertical', position: px(6) });

    assert.equal(s.update(0, 0).x.lineId, 'a');
    // 5px from 'a' (within 8px release) but 1px from 'b': must stay on 'a'.
    const held = s.update(px(5), 0).x;
    assert.equal(held.lineId, 'a');
    assert.equal(held.position, 0);
    // 9px from 'a' exceeds release: drop, then nearest-within-snap is 'b' (3px).
    const moved = s.update(px(9), 0).x;
    assert.equal(moved.lineId, 'b');
    assert.equal(moved.position, px(6));
  });
});

describe('simultaneous horizontal and vertical snapping', () => {
  it('snaps both axes independently in one update', () => {
    const s = makeSnapper();
    s.addLine({ id: 'vx', axis: 'vertical', position: px(100) });
    s.addLine({ id: 'hy', axis: 'horizontal', position: px(200) });
    const r = s.update(px(103), px(197));
    assert.deepEqual(r.x, { position: px(100), snapped: true, lineId: 'vx' });
    assert.deepEqual(r.y, { position: px(200), snapped: true, lineId: 'hy' });
  });

  it('releases one axis while the other stays snapped', () => {
    const s = makeSnapper();
    s.addLine({ id: 'vx', axis: 'vertical', position: px(100) });
    s.addLine({ id: 'hy', axis: 'horizontal', position: px(200) });
    s.update(px(100), px(200));
    const r = s.update(px(120), px(205)); // x 20px away, y 5px away (< 8px release)
    assert.equal(r.x.snapped, false);
    assert.equal(r.y.snapped, true);
    assert.equal(r.y.lineId, 'hy');
  });

  it('ignores lines on the other axis', () => {
    const s = makeSnapper();
    s.addLine({ id: 'hy', axis: 'horizontal', position: px(1) });
    const r = s.update(px(1), px(50));
    assert.equal(r.x.snapped, false); // horizontal line must not snap x
    assert.equal(r.x.position, px(1));
  });
});

describe('line deletion and movement', () => {
  it('releases the snap synchronously when the snapped line is removed', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: px(10) });
    assert.equal(s.update(px(12), 0).x.snapped, true);
    s.removeLine('v');
    assert.equal(s.snappedLineId('vertical'), null);
    const r = s.update(px(12), 0).x;
    assert.deepEqual(r, { position: px(12), snapped: false, lineId: null });
  });

  it('can snap to a remaining line right after the snapped one is deleted', () => {
    const s = makeSnapper();
    s.addLine({ id: 'gone', axis: 'vertical', position: px(10) });
    s.addLine({ id: 'stay', axis: 'vertical', position: px(14) });
    s.update(px(10), 0);
    s.removeLine('gone');
    const r = s.update(px(12), 0).x; // 2px from 'stay'
    assert.equal(r.snapped, true);
    assert.equal(r.lineId, 'stay');
  });

  it('follows the snapped line when it moves', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    s.update(0, 0);
    s.moveLine('v', px(5)); // within the 8px release band of the drag point
    const r = s.update(0, 0).x; // drag point unchanged, line moved
    assert.equal(r.snapped, true);
    assert.equal(r.position, px(5));
  });

  it('releases when the snapped line moves beyond the release threshold', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    s.update(0, 0);
    s.moveLine('v', px(9)); // now 9px away from the drag point at 0
    const r = s.update(0, 0).x;
    assert.equal(r.snapped, false);
    assert.equal(r.position, 0);
  });

  it('snaps when a line moves into the threshold', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: px(20) });
    assert.equal(s.update(0, 0).x.snapped, false);
    s.moveLine('v', px(3));
    const r = s.update(0, 0).x;
    assert.equal(r.snapped, true);
    assert.equal(r.position, px(3));
  });
});

describe('zoom scaling', () => {
  it('scales thresholds by zoom along a continuous trajectory', () => {
    // zoom 2: 4px screen threshold == 2 logical px == 128 subpixels.
    const s = makeSnapper(2);
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    let snappedAt: number | null = null;
    for (let p = px(3); p >= 0; p -= 1) {
      if (s.update(p, 0).x.snapped && snappedAt === null) snappedAt = p;
    }
    assert.equal(snappedAt, 128);
    // One subpixel beyond the boundary stays unsnapped on a fresh snapper.
    const fresh = makeSnapper(2);
    fresh.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.equal(fresh.update(129, 0).x.snapped, false);
    assert.equal(fresh.update(128, 0).x.snapped, true);
  });

  it('widens the logical range when zooming out', () => {
    // zoom 0.5: 4px screen threshold == 8 logical px.
    const s = makeSnapper(0.5);
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.equal(s.update(px(8), 0).x.snapped, true);
    const fresh = makeSnapper(0.5);
    fresh.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.equal(fresh.update(px(8) + 1, 0).x.snapped, false);
  });

  it('re-evaluates the active snap when zoom changes', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.equal(s.update(px(3), 0).x.snapped, true); // 3px screen at zoom 1
    s.setZoom(3); // now 9px screen, beyond the 8px release threshold
    const r = s.update(px(3), 0).x;
    assert.equal(r.snapped, false);
    assert.equal(r.position, px(3));
  });
});

describe('integer subpixel contract', () => {
  it('returns the exact integer line position, including fractional-px lines', () => {
    const s = makeSnapper();
    s.addLine({ id: 'v', axis: 'vertical', position: 6432 }); // 100.5 logical px
    const r = s.update(6432 + 100, 0).x;
    assert.equal(r.snapped, true);
    assert.equal(r.position, 6432);
    assert.equal(Number.isInteger(r.position), true);
  });

  it('rejects non-integer coordinates and line positions', () => {
    const s = makeSnapper();
    assert.throws(() => s.update(1.5, 0), TypeError);
    assert.throws(() => s.addLine({ id: 'v', axis: 'vertical', position: 0.5 }), TypeError);
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.throws(() => s.moveLine('v', 2.5), TypeError);
  });

  it('honours a custom subpixelsPerPixel', () => {
    const s = new Snapper({ snapThresholdPx: 4, releaseThresholdPx: 8, subpixelsPerPixel: 100 });
    s.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.equal(s.update(400, 0).x.snapped, true); // exactly 4 logical px
    const fresh = new Snapper({ snapThresholdPx: 4, releaseThresholdPx: 8, subpixelsPerPixel: 100 });
    fresh.addLine({ id: 'v', axis: 'vertical', position: 0 });
    assert.equal(fresh.update(401, 0).x.snapped, false);
  });
});

describe('option validation', () => {
  it('requires releaseThresholdPx >= snapThresholdPx', () => {
    assert.throws(() => new Snapper({ snapThresholdPx: 8, releaseThresholdPx: 4 }), RangeError);
  });

  it('rejects non-positive zoom', () => {
    const s = makeSnapper();
    assert.throws(() => s.setZoom(0), RangeError);
    assert.throws(() => new Snapper({ snapThresholdPx: 4, releaseThresholdPx: 8, zoom: -1 }), RangeError);
  });
});
