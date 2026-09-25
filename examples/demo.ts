import {
  D50,
  D65,
  PlanCache,
  compileGraph,
  linearSrgbSpace,
  makeTestImage,
  processBatch,
  srgbSpace,
  xyzSpace,
} from '../src/index.ts';
import type { GraphConfig } from '../src/index.ts';

function buildConfig(): GraphConfig {
  return {
    spaces: [srgbSpace(), linearSrgbSpace(), xyzSpace(D65, 'xyz-d65'), xyzSpace(D50, 'xyz-d50')],
    edges: [
      { from: 'srgb', to: 'linear-srgb' },
      { from: 'linear-srgb', to: 'xyz-d65' },
      // Redundant but theoretically equivalent path: verified at compile time.
      { from: 'srgb', to: 'xyz-d65' },
      // Different white points: Bradford adaptation is inserted automatically.
      { from: 'xyz-d65', to: 'xyz-d50' },
    ],
  };
}

const cache = new PlanCache();
const plan = cache.compileOrGet(buildConfig());
console.log(`plan version: ${plan.version}`);
console.log(`path srgb -> xyz-d50: ${plan.pathLabels('srgb', 'xyz-d50').join(' then ')}`);

const image = makeTestImage();
console.log(`\nimage: ${image.width}x${image.height} RGBA (${image.width * image.height} px)`);

// 1) preserve strategy: out-of-gamut values (incl. negatives) are kept.
const preserved = processBatch(image.data, plan, 'srgb', 'linear-srgb', {
  strategy: 'preserve',
  chunkSize: 5,
  onChunk: (done, totalChunks) => console.log(`  chunk ${done}/${totalChunks}`),
});
console.log('\n[preserve] srgb -> linear-srgb');
console.log(`  reversible: ${preserved.reversible}`);
console.log(`  roundtrip max=${preserved.roundtrip.maxError.toExponential(3)} mean=${preserved.roundtrip.meanError.toExponential(3)}`);
console.log(`  out-of-gamut px12 linear: [${[...preserved.pixels.slice(48, 51)].map((v) => v.toFixed(4)).join(', ')}]`);

// 2) clip strategy: out-of-range encoded values are clamped and masked.
const clipped = processBatch(image.data, plan, 'srgb', 'srgb', {
  strategy: 'clip',
  chunkSize: 5,
});
console.log('\n[clip] srgb -> srgb (image contains 1.2 / -0.2)');
console.log(`  reversible: ${clipped.reversible}`);
console.log(`  clip mask: [${[...clipped.clipMask].join(', ')}] (R=1 G=2 B=4)`);
console.log(`  roundtrip max=${clipped.roundtrip.maxError.toExponential(3)} (clipped px expected to differ)`);

// 3) repeated clip: prior mask is carried, still irreversible.
const reclipped = processBatch(clipped.pixels, plan, 'srgb', 'srgb', {
  strategy: 'clip',
  priorClipMask: clipped.clipMask,
});
console.log(`\n[re-clip] reversible: ${reclipped.reversible} (must stay false)`);

// 4) hot config swap: same graph but sRGB re-based on D50 -> new version, isolated cache.
const swapped = buildConfig();
swapped.spaces[0] = { ...swapped.spaces[0], white: [...D50] };
const plan2 = cache.compileOrGet(swapped);
console.log(`\nhot swap: v1=${plan.version} v2=${plan2.version} cacheSize=${cache.size}`);
console.log(`old plan still intact: ${cache.get(plan.version) === plan}`);

// 5) white point adaptation: D65 white lands on D50 white.
const adapted = plan.convert('xyz-d65', 'xyz-d50', [D65[0], D65[1], D65[2]]);
console.log(`\nD65 white in D50 space: [${adapted.map((v) => v.toFixed(5)).join(', ')}] (expect ~${D50})`);
