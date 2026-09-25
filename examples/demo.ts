/**
 * Runnable demo: builds a small self-made RGBA image (no Canvas, no image
 * libraries), converts it between color spaces with both gamut strategies,
 * and prints clip masks, reversibility and round-trip errors.
 *
 * Run: node examples/demo.ts   (Node >= 23.6)
 */
import {
  ColorEngine,
  Pipeline,
  builtinSpaces,
  ConversionGraph,
  processBatch,
} from '../src/index.ts';

function buildTestImage(): Uint8ClampedArray {
  // 8x8 RGBA: horizontal red->green gradient, vertical blue ramp,
  // plus a transparent pixel and pure black/white corners.
  const data = new Uint8ClampedArray(8 * 8 * 4);
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      const o = (y * 8 + x) * 4;
      data[o] = Math.round((x / 7) * 255);
      data[o + 1] = Math.round(((7 - x) / 7) * 255);
      data[o + 2] = Math.round((y / 7) * 255);
      data[o + 3] = 255;
    }
  }
  data.set([0, 0, 0, 255], 0);
  data.set([255, 255, 255, 255], 7 * 4);
  data.set([200, 100, 50, 0], (3 * 8 + 4) * 4); // fully transparent
  return data;
}

function report(label: string, result: ReturnType<typeof processBatch>): void {
  console.log(`  ${label}`);
  console.log(`    version:        ${result.version}`);
  console.log(`    chunks:         ${result.chunks}`);
  console.log(`    clipped pixels: ${result.clippedPixels}`);
  console.log(`    reversible:     ${result.reversible}`);
  if (result.roundTrip) {
    console.log(`    round-trip max: ${result.roundTrip.maxError.toExponential(3)}`);
    console.log(`    round-trip avg: ${result.roundTrip.meanError.toExponential(3)}`);
  }
}

const graph = new ConversionGraph();
for (const space of builtinSpaces()) graph.addSpace(space);
const compiled = graph.compile();
console.log(`graph compiled, fingerprint ${compiled.fingerprint}\n`);

const engine = new ColorEngine(compiled, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
const image = buildTestImage();

console.log('1) srgb -> display-p3 (preserve), 8x8 byte image, chunks of 16 px');
const p3 = engine.pipeline();
console.log(p3.describe());
report('preserve', processBatch(p3, image, { chunkSize: 16 }));

console.log('\n2) hot swap: same engine, clip strategy + prophoto (D50) target');
const wide = engine.setConfig({ source: 'srgb-linear', target: 'prophoto', strategy: 'clip' });
console.log(wide.describe());

// Out-of-gamut linear data to show the difference between strategies.
const linear = new Float32Array([
  1.3, -0.2, 0.4, 1,
  0.2, 0.5, 0.8, 1,
  2.0, 2.0, 2.0, 0,
]);
console.log('\n3) srgb-linear -> prophoto, out-of-gamut float pixels');
report('clip    ', processBatch(wide, linear));
const widePreserve = new Pipeline(compiled, { source: 'srgb-linear', target: 'prophoto', strategy: 'preserve' });
report('preserve', processBatch(widePreserve, linear));

console.log('\n4) repeated clipping keeps the irreversible mask');
const toSrgbClip = new Pipeline(compiled, { source: 'srgb-linear', target: 'srgb', strategy: 'clip' });
const identityClip = new Pipeline(compiled, { source: 'srgb', target: 'srgb', strategy: 'clip' });
const first = processBatch(toSrgbClip, linear);
const second = processBatch(identityClip, first.output, { priorMask: first.clipMask });
report('pass 1  ', first);
report('pass 2  ', second);
console.log(`    mask after pass 2: [${[...second.clipMask].join(', ')}]`);

console.log(`\ncache entries after hot swap: ${engine.cache.size} (old versions stay isolated)`);
