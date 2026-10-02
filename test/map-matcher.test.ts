import { test } from "node:test";
import assert from "node:assert/strict";
import {
  haversine,
  projectToSegment,
  interpolateAlong,
} from "../src/geometry.ts";
import { RoadNetwork } from "../src/network.ts";
import { MapMatcher, type Candidate } from "../src/matcher.ts";
import { buildGridCity, toLonLat } from "../examples/grid-city.ts";

test("geometry: haversine computes known distance", () => {
  const d = haversine({ lon: 0, lat: 0 }, { lon: 0, lat: 1 });
  assert.ok(Math.abs(d - 111195) < 300, `expected ~111195 m, got ${d}`);
});

test("geometry: projection onto segment clamps and measures", () => {
  const a = { lon: 0, lat: 0 };
  const b = { lon: 0, lat: 0.01 }; // ~1112 m north
  const mid = projectToSegment({ lon: 0.001, lat: 0.005 }, a, b);
  assert.ok(Math.abs(mid.fraction - 0.5) < 1e-6);
  assert.ok(Math.abs(mid.distance - 111.32) < 1);
  const beyond = projectToSegment({ lon: 0, lat: 0.02 }, a, b);
  assert.equal(beyond.fraction, 1);
});

test("geometry: interpolateAlong walks a polyline", () => {
  const line = [
    { lon: 0, lat: 0 },
    { lon: 0, lat: 0.01 },
  ];
  const total = haversine(line[0], line[1]);
  const { point, fraction } = interpolateAlong(line, total / 2);
  assert.ok(Math.abs(fraction - 0.5) < 1e-9);
  assert.ok(Math.abs(point.lat - 0.005) < 1e-6);
});

test("network: Dijkstra shortest path and connectivity", () => {
  const net = new RoadNetwork();
  const a = net.addNode({ lon: 0, lat: 0 });
  const b = net.addNode({ lon: 0, lat: 0.001 });
  const c = net.addNode({ lon: 0, lat: 0.002 });
  const isolated = net.addNode({ lon: 1, lat: 1 });
  net.addEdge("ab", a, b);
  net.addEdge("bc", b, c);
  const ab = net.nodeDistance(a, b);
  const bc = net.nodeDistance(b, c);
  assert.ok(Math.abs(net.shortestPath(a, c) - (ab + bc)) < 1e-6);
  assert.equal(net.shortestPath(c, a), Infinity, "directed: no way back");
  assert.equal(net.shortestPath(a, isolated), Infinity);
});

test("emission: Gaussian decreases with distance", () => {
  const net = new RoadNetwork();
  const m = new MapMatcher(net, { sigma: 20 });
  const near = m.emissionLogProb(5);
  const far = m.emissionLogProb(40);
  assert.ok(near > far);
  // Ratio matches the Gaussian kernel: log p(5) - log p(40) = (40^2-5^2)/(2*20^2)
  const expected = (40 * 40 - 5 * 5) / (2 * 20 * 20);
  assert.ok(Math.abs(near - far - expected) < 1e-9);
});

test("transition: rewards route/linear consistency, punishes detours", () => {
  const { network } = buildGridCity();
  const m = new MapMatcher(network, { beta: 10 });
  const mainIdx = network.edges.findIndex((e) => e.id === "main-1");
  const mk = (offset: number): Candidate => ({
    edgeIndex: mainIdx,
    offset,
    snapped: toLonLat(100 + offset, 0),
    distance: 0,
  });
  // 50 m forward on the same edge, GPS moved 50 m: consistent.
  const good = m.transitionLogProb(mk(10), mk(60), 50, 5);
  // GPS moved 50 m but route says 150 m: penalized by 100/beta = 10 nats.
  const bad = m.transitionLogProb(mk(10), mk(60), 150, 5);
  assert.ok(Math.abs(good - bad - 10) < 1e-9);
  // Backwards on a directed edge is impossible.
  assert.equal(m.transitionLogProb(mk(60), mk(10), 50, 5), -Infinity);
  // Speeding: 50 m in 0.5 s = 100 m/s > maxSpeed.
  assert.equal(m.transitionLogProb(mk(10), mk(60), 50, 0.5), -Infinity);
});

test("transition: disconnected candidates are unreachable", () => {
  const net = new RoadNetwork();
  const a = net.addNode({ lon: 0, lat: 0 });
  const b = net.addNode({ lon: 0, lat: 0.001 });
  const c = net.addNode({ lon: 1, lat: 1 });
  const d = net.addNode({ lon: 1, lat: 1.001 });
  net.addEdge("e1", a, b);
  net.addEdge("e2", c, d);
  const m = new MapMatcher(net);
  const c1: Candidate = { edgeIndex: 0, offset: 50, snapped: a, distance: 0 };
  const c2: Candidate = { edgeIndex: 1, offset: 0, snapped: c, distance: 0 };
  assert.equal(m.transitionLogProb(c1, c2, 0, 10), -Infinity);
});

test("match: multipath drift in urban canyon snaps to true road", () => {
  const { network, truth, trace, trueEdgeIds } = buildGridCity();
  const matcher = new MapMatcher(network, {
    sigma: 20,
    beta: 10,
    candidateRadius: 80,
  });
  const results = matcher.match(trace);
  assert.equal(results.length, trace.length);

  // Every fix snaps onto the true path (fixes sitting exactly on an
  // intersection may legally snap to either adjoining edge).
  results.forEach((r, i) => {
    const snapToTruth = haversine(
      r.candidate.snapped,
      toLonLat(truth[i].x, truth[i].y),
    );
    assert.ok(
      trueEdgeIds.has(r.edgeId) || snapToTruth < 5,
      `fix ${i} matched unexpected edge ${r.edgeId} (${snapToTruth.toFixed(1)} m off)`,
    );
  });

  // The heavily biased canyon fixes (#3..#6, 35-55 m off) stay on Main St.
  for (let i = 3; i <= 6; i++) {
    assert.match(results[i].edgeId, /^main-/);
  }
  // The final fix follows the northward turn.
  assert.equal(results[results.length - 1].edgeId, "cross-400-n");

  // Matched positions are far closer to ground truth than the raw GPS.
  let rawErr = 0;
  let matchedErr = 0;
  results.forEach((r, i) => {
    const t = toLonLat(truth[i].x, truth[i].y);
    rawErr += haversine(r.observation.point, t);
    matchedErr += haversine(r.candidate.snapped, t);
  });
  rawErr /= results.length;
  matchedErr /= results.length;
  assert.ok(
    matchedErr < rawErr / 2,
    `mean error should drop >2x (raw ${rawErr.toFixed(1)} m, matched ${matchedErr.toFixed(1)} m)`,
  );
  assert.ok(matchedErr < 15, `matched mean error ${matchedErr.toFixed(1)} m`);
});

test("match: snapped points lie on the road centerline", () => {
  const { network, trace } = buildGridCity(7);
  const matcher = new MapMatcher(network, { sigma: 20 });
  const results = matcher.match(trace);
  // Snapped positions must sit exactly on some edge geometry: re-querying
  // candidates at the snapped point yields distance ~0 on that edge.
  for (const r of results) {
    const cands = matcher.findCandidates(r.candidate.snapped);
    const onEdge = cands.find((c) => c.edgeIndex === r.candidate.edgeIndex);
    assert.ok(onEdge, "snapped point should project onto its own edge");
    assert.ok(onEdge.distance < 1e-6, `residual ${onEdge.distance}`);
  }
});

test("match: rejects traces with no nearby road", () => {
  const { network } = buildGridCity();
  const matcher = new MapMatcher(network, { candidateRadius: 50 });
  assert.throws(
    () =>
      matcher.match([
        { point: toLonLat(0, 5000), timestamp: 0 }, // 5 km off the map
      ]),
    /No road candidate/,
  );
});
