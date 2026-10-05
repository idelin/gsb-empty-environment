import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GrayImage, RgbaImage } from '../src/image.ts';
import { makeSE, LINE3_RIGHT, SQUARE3 } from '../src/se.ts';
import type { StructuringElement } from '../src/se.ts';
import { dilate, erode, open, close } from '../src/ops.ts';
import type { BoundaryMode } from '../src/types.ts';

/** Independent naive reference, straight from the definitions. */
function naiveDilate(img: GrayImage, se: StructuringElement, boundary: BoundaryMode): GrayImage {
  const out = new GrayImage(img.width, img.height);
  for (let y = 0; y < img.height; y++) {
    for (let x = 0; x < img.width; x++) {
      let v = 0;
      for (const [dx, dy] of se.offsets) {
        const sx = x - dx;
        const sy = y - dy;
        if (sx < 0 || sy < 0 || sx >= img.width || sy >= img.height) {
          if (boundary === 'zero') continue;
          throw new Error('naive ref only supports zero boundary');
        }
        v = Math.max(v, img.get(sx, sy));
      }
      out.set(x, y, v);
    }
  }
  return out;
}

function pattern(): GrayImage {
  return GrayImage.fromAscii(
    [
      '..........',
      '.##..##...',
      '.##...#...',
      '.....#....',
      '..####....',
      '..#.......',
      '..#..##...',
      '.....##...',
      '..........',
      '.#........',
    ],
    { '.': 0, '#': 255 },
  );
}

test('non-symmetric SE dilation stamps in the drawn direction', () => {
  const img = new GrayImage(6, 5);
  img.set(2, 2, 255);

  const right = makeSE([[0, 0], [1, 0], [2, 0]]); // origin at left end
  const out = dilate(img, { se: right });
  assert.equal(out.get(2, 2), 255);
  assert.equal(out.get(3, 2), 255);
  assert.equal(out.get(4, 2), 255);
  assert.equal(out.get(1, 2), 0, 'must not grow against the drawn direction');
  assert.equal(out.get(2, 1), 0);

  const centered = makeSE([[0, 0], [1, 0], [2, 0]], [1, 0]); // origin in middle
  const out2 = dilate(img, { se: centered });
  assert.equal(out2.get(1, 2), 255);
  assert.equal(out2.get(2, 2), 255);
  assert.equal(out2.get(3, 2), 255);
  assert.equal(out2.get(4, 2), 0);
});

test('non-symmetric SE erosion respects direction', () => {
  const img = new GrayImage(6, 1);
  for (const x of [1, 2, 3, 4]) img.set(x, 0, 255);
  const se = makeSE([[0, 0], [1, 0]]); // needs self and right neighbor
  const out = erode(img, { se });
  assert.deepEqual(
    [0, 1, 2, 3, 4, 5].map((x) => out.get(x, 0)),
    [0, 255, 255, 255, 0, 0],
  );
});

test('dilate matches naive reference on a pattern', () => {
  const img = pattern();
  for (const se of [SQUARE3, LINE3_RIGHT, makeSE([[0, 0], [1, 1], [-2, 1]], [0, 0])]) {
    const got = dilate(img, { se });
    const want = naiveDilate(img, se, 'zero');
    assert.ok(got.equals(want));
  }
});

test('boundary modes differ at the border as specified', () => {
  const img = GrayImage.fromAscii(['abc'], { a: 10, b: 20, c: 30 });
  const se = makeSE([[-1, 0], [0, 0], [1, 0]]);
  const modes: BoundaryMode[] = ['zero', 'max', 'clamp', 'skip'];
  const got = modes.map((m) => {
    const o = dilate(img, { se, boundary: m });
    return [o.get(0, 0), o.get(2, 0)];
  });
  assert.deepEqual(got[0], [20, 30], 'zero');
  assert.deepEqual(got[1], [255, 255], 'max');
  assert.deepEqual(got[2], [20, 30], 'clamp');
  assert.deepEqual(got[3], [20, 30], 'skip');

  const e = modes.map((m) => {
    const o = erode(img, { se, boundary: m });
    return o.get(0, 0);
  });
  assert.deepEqual(e, [0, 10, 10, 10], 'erosion at left border per mode');
});

test('opening never grows foreground, closing never shrinks it', () => {
  const img = pattern();
  for (const se of [SQUARE3, LINE3_RIGHT, makeSE([[0, 0], [1, 0], [0, 1]])]) {
    const opened = open(img, { se });
    const closed = close(img, { se });
    for (let i = 0; i < img.data.length; i++) {
      assert.ok(opened.data[i] <= img.data[i], `open grew foreground at ${i}`);
      assert.ok(closed.data[i] >= img.data[i], `close shrank foreground at ${i}`);
    }
    // idempotence
    assert.ok(open(opened, { se }).equals(opened));
    assert.ok(close(closed, { se }).equals(closed));
  }
});

test('label mask passes pixels through untouched', () => {
  const img = pattern();
  const label = new Uint8Array(img.width * img.height).fill(1);
  label[2 * img.width + 3] = 0; // (3,2) is foreground in the pattern
  label[0] = 0; // (0,0) background
  const out = dilate(img, { se: SQUARE3, label });
  assert.equal(out.get(3, 2), img.get(3, 2));
  assert.equal(out.get(0, 0), img.get(0, 0));
  // a labeled pixel still gets dilated
  assert.equal(out.get(3, 1), 255);
});

test('RGBA pipeline: luminance conversion and back', () => {
  const rgba = RgbaImage.fromAscii(['.#r', '###'], {
    '.': [0, 0, 0, 255],
    '#': [255, 255, 255, 255],
    r: [255, 0, 0, 255],
  });
  const gray = rgba.toGray();
  assert.equal(gray.get(0, 0), 0);
  assert.equal(gray.get(1, 0), 255);
  assert.equal(gray.get(2, 0), 76, 'pure red -> integer luminance');
  const back = gray.toRgba();
  assert.deepEqual(back.get(1, 0), [255, 255, 255, 255]);
  assert.deepEqual(back.get(2, 0), [76, 76, 76, 255]);
});
