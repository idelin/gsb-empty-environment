import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { kernel, makeChain, stage, type StageSpec } from '../src/kernels.ts';
import { CancelledError, runPipeline } from '../src/pipeline.ts';
import { convolveReference } from '../src/reference.ts';
import { ConvolverSession } from '../src/session.ts';
import {
  UNLIMITED_BUDGET,
  assertRasterExact,
  cleanupDir,
  mulberry32,
  randomRaster,
  tempWorkDir,
} from './helpers.ts';

const blurStages = (): StageSpec[] => [
  stage(kernel([0.25, 0.5, 0.25]), kernel([0.25, 0.5, 0.25]), 'clamp', 'clamp'),
  stage(kernel([0.25, 0.5, 0.25]), kernel([0.25, 0.5, 0.25]), 'clamp', 'clamp'),
];

function assertNoTempArtifacts(dir: string): void {
  const entries = existsSync(dir) ? readdirSync(dir) : [];
  for (const e of entries) {
    assert.ok(!e.startsWith('.spill-'), `leftover spill dir ${e}`);
    assert.ok(!e.startsWith('.index.json.'), `leftover temp index ${e}`);
  }
}

function assertNoPartialTiles(dir: string): void {
  const tilesDir = join(dir, 'tiles');
  if (existsSync(tilesDir)) {
    assert.deepEqual(readdirSync(tilesDir), [], 'no partial output tiles may remain');
  }
}

test('cancellation releases cache and temp files and publishes nothing', () => {
  const dir = tempWorkDir('tileconv-cancel-');
  try {
    const source = randomRaster(64, 64, 9);
    const chain = makeChain(1, blurStages());
    const controller = new AbortController();
    assert.throws(
      () =>
        runPipeline({
          source,
          chain,
          tileSize: 8,
          mode: 'fused',
          budgetBytes: UNLIMITED_BUDGET,
          workDir: dir,
          signal: controller.signal,
          hooks: {
            onTileComputed: (_key, computed) => {
              if (computed === 2) controller.abort();
            },
          },
        }),
      (e: unknown) => e instanceof CancelledError,
    );
    assert.ok(!existsSync(join(dir, 'index.json')), 'no index after cancel');
    assertNoTempArtifacts(dir);
    assertNoPartialTiles(dir);
  } finally {
    cleanupDir(dir);
  }
});

test('failure releases cache and temp files and publishes nothing', () => {
  const dir = tempWorkDir('tileconv-fail-');
  try {
    const source = randomRaster(32, 32, 10);
    const chain = makeChain(1, blurStages());
    assert.throws(
      () =>
        runPipeline({
          source,
          chain,
          tileSize: 8,
          mode: 'fused',
          budgetBytes: UNLIMITED_BUDGET,
          workDir: dir,
          maxRetries: 0,
          hooks: { failOnceOnTile: '1,1' },
        }),
      /injected fault/,
    );
    assert.ok(!existsSync(join(dir, 'index.json')), 'no index after failure');
    assertNoTempArtifacts(dir);
    assertNoPartialTiles(dir);
  } finally {
    cleanupDir(dir);
  }
});

test('root index is published atomically and matches persisted tiles', () => {
  const dir = tempWorkDir('tileconv-publish-');
  try {
    const source = randomRaster(24, 16, 12);
    const chain = makeChain(3, blurStages());
    const result = runPipeline({
      source,
      chain,
      tileSize: 8,
      mode: 'fused',
      budgetBytes: UNLIMITED_BUDGET,
      workDir: dir,
    });
    const index = JSON.parse(readFileSync(result.indexPath, 'utf8'));
    assert.equal(index.version, 3);
    assert.equal(index.width, 24);
    assert.equal(index.height, 16);
    assert.equal(index.stats.ops, result.ops);
    assert.equal(index.stats.peakMemoryBytes, result.peakMemoryBytes);
    assert.equal(index.tiles.length, 3 * 2);
    for (const entry of index.tiles) {
      const bytes = readFileSync(join(dir, entry.file));
      const digest = createHash('sha256').update(bytes).digest('hex');
      assert.equal(entry.sha256, digest, `checksum of ${entry.file}`);
    }
    assertNoTempArtifacts(dir);
  } finally {
    cleanupDir(dir);
  }
});

test('single-tile edit invalidates exactly the two-layer halo neighbourhood', () => {
  const dir = tempWorkDir('tileconv-session-');
  try {
    const source = randomRaster(32, 32, 21);
    const session = new ConvolverSession(
      { source, tileSize: 8, budgetBytes: UNLIMITED_BUDGET },
      blurStages(),
    );
    session.computeAll(dir);
    const before = new Float32Array(session.getOutput().data);

    const rand = mulberry32(99);
    const patch = new Float32Array(8 * 8 * 4);
    for (let i = 0; i < patch.length; i++) patch[i] = rand();
    session.modifySourceTile(2, 2, patch);

    const affected = session.affectedTiles(2, 2).sort();
    const expected: string[] = [];
    for (const ty of [1, 2, 3]) for (const tx of [1, 2, 3]) expected.push(`${tx},${ty}`);
    assert.deepEqual(affected, expected.sort());

    session.recomputeTiles(affected);
    const fresh = convolveReference(source, session.currentChain);
    assertRasterExact(session.getOutput(), fresh, 'after local recompute');

    const out = session.getOutput().data;
    for (let ty = 0; ty < 4; ty++) {
      for (let tx = 0; tx < 4; tx++) {
        if (tx >= 1 && ty >= 1) continue;
        for (let y = ty * 8; y < ty * 8 + 8; y++) {
          for (let x = tx * 8; x < tx * 8 + 8; x++) {
            const off = (y * 32 + x) * 4;
            for (let c = 0; c < 4; c++) {
              assert.equal(
                out[off + c],
                before[off + c],
                `untouched tile (${tx},${ty}) changed at (${x},${y})`,
              );
            }
          }
        }
      }
    }
  } finally {
    cleanupDir(dir);
  }
});

test('retry after failure never mixes old and new kernel versions', () => {
  const dir = tempWorkDir('tileconv-retry-');
  try {
    const source = randomRaster(24, 24, 31);
    const v1Stages = blurStages();
    const session = new ConvolverSession(
      { source, tileSize: 8, budgetBytes: UNLIMITED_BUDGET },
      v1Stages,
    );
    const v2Stages: StageSpec[] = [
      stage(kernel([0.5, 0.5], 0), kernel([1], 0), 'mirror', 'clamp'),
    ];
    const result = session.computeAll(dir, {
      failOnceOnTile: '1,1',
      onBeforeTileRetry: () => {
        session.swapKernels(v2Stages);
      },
    });
    assert.equal(result.version, 1, 'run must complete on the v1 snapshot');
    assert.equal(session.currentVersion, 2, 'chain was swapped mid-run');
    assertRasterExact(
      result.output,
      convolveReference(source, makeChain(1, v1Stages)),
      'retried tile must use the v1 snapshot, not v2',
    );

    session.recomputeStale();
    assertRasterExact(
      session.getOutput(),
      convolveReference(source, makeChain(2, v2Stages)),
      'after recomputeStale',
    );
    assert.equal(session.tileVersion('0,0'), 2);
  } finally {
    cleanupDir(dir);
  }
});
