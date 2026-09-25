import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ColorEngine, PipelineCache, type PipelineConfig } from '../src/index.ts';
import { standardCompiledGraph, standardGraph } from './helpers.ts';

const baseConfig: PipelineConfig = { source: 'srgb', target: 'display-p3', strategy: 'preserve' };

test('cache is bound to the config version: same config hits, new config misses', () => {
  const cache = new PipelineCache(standardCompiledGraph());
  const first = cache.getOrBuild(baseConfig);
  const second = cache.getOrBuild({ ...baseConfig });
  assert.equal(first.cacheHit, false);
  assert.equal(second.cacheHit, true);
  assert.equal(first.pipeline, second.pipeline);
  assert.equal(cache.size, 1);

  const other = cache.getOrBuild({ ...baseConfig, strategy: 'clip' });
  assert.equal(other.cacheHit, false);
  assert.notEqual(other.version, first.version);
  assert.equal(cache.size, 2);
});

test('hot config swap resolves to a new version without touching old entries', () => {
  const engine = new ColorEngine(standardCompiledGraph(), baseConfig);
  const before = engine.pipeline();
  const beforeVersion = before.version;

  const after = engine.setConfig({ source: 'srgb', target: 'prophoto', strategy: 'clip' });
  assert.notEqual(after.version, beforeVersion);
  assert.equal(engine.pipeline(), after, 'hot swap takes effect immediately');

  const oldEntry = engine.cache.get(beforeVersion);
  assert.equal(oldEntry, before, 'old version cache entry stays intact and isolated');
  assert.equal(oldEntry?.config.strategy, 'preserve', 'cached pipeline was not mutated');
  assert.equal(engine.cache.size, 2);
});

test('old-version cache entries are isolated by versionTag and graph fingerprint', () => {
  const cache = new PipelineCache(standardCompiledGraph());
  const v1 = cache.getOrBuild({ ...baseConfig, versionTag: 'lut-v1' });
  const v2 = cache.getOrBuild({ ...baseConfig, versionTag: 'lut-v2' });
  assert.notEqual(v1.version, v2.version);
  assert.equal(cache.get(v1.version), v1.pipeline);
  assert.equal(cache.get(v2.version), v2.pipeline);
  assert.equal(cache.get('deadbeefdeadbeef'), undefined);

  // A graph with an extra explicit edge fingerprints differently, so a
  // cache built on it can never collide with the old graph's versions.
  const graphWithEdge = standardGraph();
  graphWithEdge.addEdge({
    from: 'srgb',
    to: 'display-p3',
    matrix: graphWithEdge.hubMatrix('srgb', 'display-p3'),
  });
  const otherCache = new PipelineCache(graphWithEdge.compile());
  const rebuilt = otherCache.getOrBuild({ ...baseConfig, versionTag: 'lut-v1' });
  assert.notEqual(rebuilt.version, v1.version, 'graph change must change the version');
});

test('batch results carry the version of the pipeline that produced them', async () => {
  const { processBatch } = await import('../src/index.ts');
  const engine = new ColorEngine(standardCompiledGraph(), baseConfig);
  const image = new Uint8ClampedArray([255, 0, 0, 255]);
  const first = processBatch(engine.pipeline(), image);
  engine.setConfig({ ...baseConfig, strategy: 'clip' });
  const second = processBatch(engine.pipeline(), image);
  assert.notEqual(first.version, second.version);
  assert.equal(first.version, engine.cache.get(first.version)?.version);
});
