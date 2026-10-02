import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DensityAnalyzer, MS_PER_HOUR } from '../src/index.ts';

const grid = { minLat: 39.0, minLng: 116.0, maxLat: 40.0, maxLng: 117.0, tileSizeMeters: 1000 };

function makeAnalyzer(capacityHours = 24 * 31): DensityAnalyzer {
  return new DensityAnalyzer({ grid, capacityHours });
}

// 2026-10-02 is a Friday. Use fixed hour buckets for determinism.
const H0 = Math.floor(Date.UTC(2026, 9, 2, 10, 0, 0) / MS_PER_HOUR); // Friday 18:00 CST
const T0 = H0 * MS_PER_HOUR;

test('streaming points accumulate into the right cell and hour', () => {
  const a = makeAnalyzer();
  a.addPoint(39.5, 116.5, T0);
  a.addPoint(39.5, 116.5, T0 + 30 * 60_000); // same hour
  a.addPoint(39.5, 116.5, T0, 2.5); // weighted

  const top = a.queryTopK(T0, T0 + MS_PER_HOUR, 1);
  assert.equal(top.length, 1);
  assert.equal(top[0].weight, 4.5);
  assert.equal(top[0].rank, 1);

  // The reported center must fall inside the cell containing the point.
  const cell = a.grid.cellOf(39.5, 116.5);
  assert.equal(top[0].cell, cell);
  const center = a.grid.centerOf(cell);
  assert.ok(Math.abs(center.lat - top[0].lat) < 1e-12);
});

test('time window filters hours correctly', () => {
  const a = makeAnalyzer();
  a.addPoint(39.1, 116.1, T0, 5); // hour H0
  a.addPoint(39.9, 116.9, T0 + 2 * MS_PER_HOUR, 9); // hour H0+2

  const h0 = a.queryTopK(T0, T0 + MS_PER_HOUR, 2);
  assert.equal(h0.length, 1);
  assert.equal(h0[0].weight, 5);

  const all = a.queryTopK(T0, T0 + 3 * MS_PER_HOUR, 2);
  assert.equal(all.length, 2);
  assert.equal(all[0].weight, 9);
  assert.equal(all[1].weight, 5);
});

test('points outside the bbox are dropped and counted', () => {
  const a = makeAnalyzer();
  assert.equal(a.addPoint(38.0, 116.5, T0), false);
  assert.equal(a.addPoint(39.5, 116.5, T0), true);
  assert.equal(a.dropped, 1);
  assert.equal(a.stats().pointsAdded, 1);
});

test('memory stays bounded under eviction pressure', () => {
  const capacity = 48;
  const a = makeAnalyzer(capacity);
  // Stream 30 days of hourly points = 720 distinct hours >> capacity.
  for (let h = 0; h < 720; h++) {
    a.addPoint(39.5, 116.5, T0 + h * MS_PER_HOUR);
  }
  const s = a.stats();
  assert.equal(s.hoursStored, capacity);
  assert.equal(s.evictions, 720 - capacity);
  assert.equal(s.pointsAdded, 720);
  assert.equal(s.cubeMemoryBytes, capacity * a.grid.cellCount * 8);
});

test('top-10 hotspots over a simulated evening peak', () => {
  const a = makeAnalyzer();
  // Two hotspots: strong at (39.95, 116.45), weaker at (39.05, 116.05).
  for (let i = 0; i < 100; i++) a.addPoint(39.95, 116.45, T0 + i * 1000);
  for (let i = 0; i < 40; i++) a.addPoint(39.05, 116.05, T0 + i * 1000);
  // Background noise elsewhere.
  for (let i = 0; i < 50; i++) {
    a.addPoint(39.2 + (i % 5) * 0.1, 116.2 + (i % 7) * 0.1, T0 + i * 60_000);
  }

  const top = a.queryTopK(T0, T0 + 2 * MS_PER_HOUR, 10);
  assert.ok(top.length <= 10);
  assert.equal(top[0].weight, 100);
  assert.equal(top[1].weight, 40);
  for (let i = 1; i < top.length; i++) {
    assert.ok(top[i - 1].weight >= top[i].weight);
    assert.equal(top[i].rank, i + 1);
  }
});

test('million-point ingest stays fast and query is sub-second', () => {
  const a = makeAnalyzer();
  const n = 1_000_000;
  const start = performance.now();
  for (let i = 0; i < n; i++) {
    // Pseudo-random but deterministic spread across the city, across 24h.
    const lat = 39.0 + ((i * 2654435761) % 1000) / 1000;
    const lng = 116.0 + ((i * 40503) % 1000) / 1000;
    a.addPoint(lat, lng, T0 + (i % 24) * MS_PER_HOUR);
  }
  const ingestMs = performance.now() - start;

  const qStart = performance.now();
  const top = a.queryTopK(T0 + 18 * MS_PER_HOUR, T0 + 20 * MS_PER_HOUR, 10);
  const queryMs = performance.now() - qStart;

  assert.equal(a.stats().pointsAdded, n);
  assert.equal(top.length, 10);
  assert.ok(ingestMs < 10_000, `ingest too slow: ${ingestMs}ms`);
  assert.ok(queryMs < 1_000, `query too slow: ${queryMs}ms`);
});
