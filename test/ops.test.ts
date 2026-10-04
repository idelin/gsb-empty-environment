import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  StructuringElement,
  localOpPlane,
  makePlane,
  planeFromAscii,
  planeToAscii,
  type Plane,
} from '../src/index.ts';

function randomPlane(width: number, height: number, density: number, seed: number): Plane {
  let s = seed >>> 0;
  const rnd = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const plane = makePlane(width, height);
  for (let i = 0; i < plane.data.length; i++) plane.data[i] = rnd() < density ? 1 : 0;
  return plane;
}

function foregroundLE(a: Plane, b: Plane): boolean {
  for (let i = 0; i < a.data.length; i++) if (a.data[i] > 0 && b.data[i] === 0) return false;
  return true;
}

test('asymmetric SE dilates along its own direction, anchored at the origin', () => {
  const src = planeFromAscii(['#.....']);
  const right = new StructuringElement([[0, 0], [1, 0], [2, 0]], [0, 0]);
  assert.deepEqual(planeToAscii(localOpPlane('dilate', src, right)), ['###...']);
  // same offsets, origin at the far end: the stamp extends towards -x instead
  const left = new StructuringElement([[0, 0], [1, 0], [2, 0]], [2, 0]);
  const src2 = planeFromAscii(['.....#']);
  assert.deepEqual(planeToAscii(localOpPlane('dilate', src2, left)), ['...###']);
  // vertical asymmetric SE grows downwards only
  const down = new StructuringElement([[0, 0], [0, 1]], [0, 0]);
  const col = planeFromAscii(['#', '.', '.']);
  assert.deepEqual(planeToAscii(localOpPlane('dilate', col, down)), ['#', '#', '.']);
});

test('erosion uses the SE unreflected; duality holds via the reflected SE', () => {
  const se = new StructuringElement([[0, 0], [1, 0], [2, 0]], [0, 0]);
  assert.deepEqual(planeToAscii(localOpPlane('erode', planeFromAscii(['###..']), se)), ['#....']);
  const A = planeFromAscii(['.##..', '..#..', '###.#', '.....', '..##.']);
  const neg: Plane = { width: 5, height: 5, data: A.data.map((v) => (v ? 0 : 1)) as Uint8Array };
  const dual = localOpPlane('dilate', neg, se.reflected(), 'one'); // negated image => negated boundary
  const e = localOpPlane('erode', A, se, 'zero');
  for (let i = 0; i < e.data.length; i++) assert.equal(e.data[i], dual.data[i] > 0 ? 0 : 1);
});

test('opening never adds foreground, closing never removes foreground', () => {
  const se = StructuringElement.cross(1);
  const sq = StructuringElement.square(1);
  for (let seed = 1; seed <= 20; seed++) {
    const img = randomPlane(17, 11, 0.35, seed);
    for (const k of [se, sq]) {
      // open is anti-extensive when dilation sees no phantom foreground
      // outside the image ('zero' / 'ignore'); 'one' / 'clamp' legitimately
      // grow the border itself.
      for (const boundary of ['zero', 'ignore'] as const) {
        const opened = localOpPlane('open', img, k, boundary);
        assert.ok(foregroundLE(opened, img), `open grew foreground (seed ${seed}, ${boundary})`);
      }
      // close is extensive when erosion is unconstrained outside ('one' / 'ignore').
      for (const boundary of ['one', 'ignore'] as const) {
        const closed = localOpPlane('close', img, k, boundary);
        assert.ok(foregroundLE(img, closed), `close shrank foreground (seed ${seed}, ${boundary})`);
      }
    }
  }
});

test('open/close equal naive erode-then-dilate composition', () => {
  const se = new StructuringElement([[0, 0], [1, 0], [0, 1]], [0, 0]); // asymmetric L
  const img = randomPlane(13, 9, 0.4, 42);
  const opened = localOpPlane('open', img, se, 'clamp');
  const refOpen = localOpPlane('dilate', localOpPlane('erode', img, se, 'clamp'), se, 'clamp');
  assert.deepEqual([...opened.data], [...refOpen.data]);
  const closed = localOpPlane('close', img, se, 'clamp');
  const refClose = localOpPlane('erode', localOpPlane('dilate', img, se, 'clamp'), se, 'clamp');
  assert.deepEqual([...closed.data], [...refClose.data]);
});

test('boundary modes are honoured per stage', () => {
  const se = StructuringElement.square(1);
  const empty = planeFromAscii(['...', '...', '...']);
  // 'one' boundary: erosion sees foreground outside, border stays 1
  const e1 = localOpPlane('erode', empty, se, 'one');
  assert.deepEqual(planeToAscii(e1), ['...', '...', '...'].map((r) => r));
  const e1b = localOpPlane('erode', planeFromAscii(['###', '###', '###']), se, 'one');
  assert.deepEqual(planeToAscii(e1b), ['###', '###', '###']);
  // 'zero' boundary shrinks at the border
  const e0 = localOpPlane('erode', planeFromAscii(['###', '###', '###']), se, 'zero');
  assert.deepEqual(planeToAscii(e0), ['...', '.#.', '...']);
  // 'ignore' boundary: missing neighbours do not constrain erosion
  const ei = localOpPlane('erode', planeFromAscii(['###', '###', '###']), se, 'ignore');
  assert.deepEqual(planeToAscii(ei), ['###', '###', '###']);
});

test('label mask gates writes; masked-out labels pass the input through', () => {
  const se = StructuringElement.square(1);
  const img = planeFromAscii(['#..', '...', '..#']);
  const labels = planeFromAscii(['...', '...', '...']);
  labels.data[8] = 7; // bottom-right pixel has label 7
  const out = localOpPlane('dilate', img, se, 'zero', { plane: labels, mask: new Set([0]) });
  // label-7 pixel must keep its input value even though dilation would set it
  assert.equal(out.data[8], 1); // input was 1 there
  const img2 = planeFromAscii(['...', '...', '...']);
  const out2 = localOpPlane('dilate', img2, se, 'zero', { plane: labels, mask: new Set([0]) });
  assert.equal(out2.data[8], 0);
  assert.equal(out2.data[0], 0);
});
