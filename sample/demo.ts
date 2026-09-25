import { writeFileSync } from 'node:fs';
import { TransformStack } from '../src/matrix.ts';
import { PathBuilder } from '../src/path.ts';
import {
  applyMask,
  fillPath,
  invertMask,
  maskDifference,
  maskIntersect,
  maskUnion,
} from '../src/raster.ts';
import type { Mask } from '../src/raster.ts';
import { referenceMask } from '../src/reference.ts';
import { RgbaImage } from '../src/image.ts';

const WIDTH = 48;
const HEIGHT = 36;
const TOLERANCE = 0.08;

function starPath(outer: number, inner: number, points: number): PathBuilder {
  const path = new PathBuilder();
  for (let i = 0; i < points * 2; i++) {
    const radius = i % 2 === 0 ? outer : inner;
    const angle = (Math.PI * i) / points - Math.PI / 2;
    const x = radius * Math.cos(angle);
    const y = radius * Math.sin(angle);
    if (i === 0) path.moveTo(x, y);
    else path.lineTo(x, y);
  }
  return path.closePath();
}

function maxAbsDiff(a: Mask, b: Mask): number {
  let max = 0;
  for (let i = 0; i < a.data.length; i++) {
    max = Math.max(max, Math.abs(a.data[i] - b.data[i]));
  }
  return max;
}

function bounds(mask: Mask): { min: number; max: number } {
  let min = Infinity;
  let max = -Infinity;
  for (const v of mask.data) {
    if (v < min) min = v;
    if (v > max) max = v;
  }
  return { min, max };
}

function ascii(mask: Mask): string {
  const ramp = ' .:-=+*#%@';
  const lines: string[] = [];
  for (let y = 0; y < mask.height; y++) {
    let line = '';
    for (let x = 0; x < mask.width; x++) {
      const v = mask.data[y * mask.width + x];
      line += ramp[Math.min(ramp.length - 1, Math.floor(v * ramp.length))];
    }
    lines.push(line);
  }
  return lines.join('\n');
}

const stackA = new TransformStack();
stackA.translate(18, 16);
stackA.rotate(Math.PI / 7);
stackA.save();
stackA.scale(1.2, 0.9);
const contoursA = new PathBuilder()
  .moveTo(-9, 0)
  .cubicTo(-9, -8, 9, -8, 9, 0)
  .cubicTo(9, 8, -9, 8, -9, 0)
  .closePath()
  .flatten(stackA.current);
stackA.restore();

const stackB = new TransformStack();
stackB.translate(33, 19);
stackB.rotate(-Math.PI / 10);
const contoursB = starPath(10, 4.5, 5).flatten(stackB.current);

const region = { x: 4, y: 4, width: 40, height: 28 };

const maskA = fillPath(contoursA, WIDTH, HEIGHT);
const maskB = fillPath(contoursB, WIDTH, HEIGHT);
const union = maskUnion(maskA, maskB);
const intersect = maskIntersect(maskA, maskB);
const difference = maskDifference(maskA, maskB);
const inverse = invertMask(maskB, region);
const chained = applyMask(union, inverse, maskA);

const refA = referenceMask(contoursA, WIDTH, HEIGHT, 32);
const refB = referenceMask(contoursB, WIDTH, HEIGHT, 32);
const refUnion = maskUnion(refA, refB);
const refIntersect = maskIntersect(refA, refB);
const refDifference = maskDifference(refA, refB);
const refInverse = invertMask(refB, region);
const refChained = applyMask(refUnion, refInverse, refA);

const checks: Array<[string, Mask, Mask]> = [
  ['blob mask (nested transform)', maskA, refA],
  ['star mask (nested transform)', maskB, refB],
  ['union', union, refUnion],
  ['intersect', intersect, refIntersect],
  ['difference', difference, refDifference],
  ['inverse mask (region only)', inverse, refInverse],
  ['chained masks (union * inverse * A)', chained, refChained],
];

console.log(`canvas ${WIDTH}x${HEIGHT}, reference = 32x32 supersampled winding rule, tolerance = ${TOLERANCE}\n`);
let allPassed = true;
for (const [name, actual, expected] of checks) {
  const error = maxAbsDiff(actual, expected);
  const { min, max } = bounds(actual);
  const inRange = min >= 0 && max <= 1;
  const passed = error <= TOLERANCE && inRange;
  allPassed = allPassed && passed;
  console.log(
    `${passed ? 'PASS' : 'FAIL'}  ${name.padEnd(34)} maxErr=${error.toFixed(4)}  range=[${min.toFixed(3)}, ${max.toFixed(3)}]`,
  );
}

const image = new RgbaImage(WIDTH, HEIGHT, [255, 255, 255, 255]);
image.composite(union, [40, 90, 220, 255]);
image.composite(chained, [230, 60, 60, 200]);

const refImage = new RgbaImage(WIDTH, HEIGHT, [255, 255, 255, 255]);
refImage.composite(refUnion, [40, 90, 220, 255]);
refImage.composite(refChained, [230, 60, 60, 200]);

let maxChannelDiff = 0;
for (let i = 0; i < image.data.length; i++) {
  maxChannelDiff = Math.max(maxChannelDiff, Math.abs(image.data[i] - refImage.data[i]));
}
const imagePassed = maxChannelDiff <= 21;
allPassed = allPassed && imagePassed;
console.log(`${imagePassed ? 'PASS' : 'FAIL'}  composited RGBA image vs reference   maxChannelDiff=${maxChannelDiff}\n`);

console.log('union mask preview:');
console.log(ascii(union));
console.log('\nchained mask preview (union * inverse(region) * A):');
console.log(ascii(chained));

const header = `P3\n${WIDTH} ${HEIGHT}\n255\n`;
const pixels: string[] = [];
for (let i = 0; i < WIDTH * HEIGHT; i++) {
  pixels.push(`${image.data[i * 4]} ${image.data[i * 4 + 1]} ${image.data[i * 4 + 2]}`);
}
const outPath = new URL('./output.ppm', import.meta.url);
writeFileSync(outPath, header + pixels.join('\n') + '\n');
console.log(`\nwrote ${outPath.pathname}`);
console.log(allPassed ? '\nALL CHECKS PASSED' : '\nSOME CHECKS FAILED');
process.exitCode = allPassed ? 0 : 1;
