import assert from 'node:assert/strict';
import test from 'node:test';
import { haversineM, projectToSegment } from '../src/geometry.ts';

test('haversine: 已知距离（上海人民广场 → 外滩约 1.6km）', () => {
  const d = haversineM({ lat: 31.2304, lon: 121.4737 }, { lat: 31.2397, lon: 121.4906 });
  assert.ok(Math.abs(d - 1940) < 120, `got ${d}`);
});

test('haversine: 同一点距离为 0，对称', () => {
  const a = { lat: 31.23, lon: 121.47 };
  assert.equal(haversineM(a, a), 0);
  const b = { lat: 31.24, lon: 121.48 };
  assert.ok(Math.abs(haversineM(a, b) - haversineM(b, a)) < 1e-9);
});

test('projectToSegment: 垂足落在线段内', () => {
  // 路段沿纬线向东约 100m，点在其北侧约 30m。
  const a = { lat: 0, lon: 0 };
  const b = { lat: 0, lon: 0.001 };
  const p = { lat: 0.00027, lon: 0.0005 };
  const proj = projectToSegment(p, a, b);
  assert.ok(Math.abs(proj.distanceM - 30) < 1, `got ${proj.distanceM}`);
  assert.ok(Math.abs(proj.fraction - 0.5) < 0.01);
  assert.ok(Math.abs(proj.alongM - haversineM(a, b) / 2) < 1);
});

test('projectToSegment: 投影超出端点时被截断', () => {
  const a = { lat: 0, lon: 0 };
  const b = { lat: 0, lon: 0.001 };
  const p = { lat: 0.0005, lon: -0.002 }; // 在 a 端外侧
  const proj = projectToSegment(p, a, b);
  assert.equal(proj.fraction, 0);
  assert.ok(Math.abs(proj.alongM) < 1e-9);
  assert.ok(Math.abs(proj.distanceM - haversineM(p, a)) < 0.5);
});
