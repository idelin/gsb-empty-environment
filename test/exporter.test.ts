import { test } from "node:test";
import assert from "node:assert/strict";
import { TokenBucketLimiter } from "../src/limiter.ts";
import {
  renderPrometheus,
  resolveMetricsResponse,
  startMetricsServer,
  PROMETHEUS_CONTENT_TYPE,
} from "../src/exporter.ts";

function parseMetrics(text: string): Map<string, number> {
  const map = new Map<string, number>();
  for (const line of text.split("\n")) {
    if (!line || line.startsWith("#")) continue;
    const idx = line.lastIndexOf(" ");
    map.set(line.slice(0, idx), Number(line.slice(idx + 1)));
  }
  return map;
}

test("renderPrometheus emits valid exposition with conserved totals", () => {
  const limiter = new TokenBucketLimiter({
    capacity: 4,
    refillPerSec: 1,
    nowMs: () => 0,
  });
  for (let i = 0; i < 6; i++) limiter.tryAcquire(); // 4 allowed, 2 dropped
  const text = renderPrometheus(limiter.snapshot());

  // HELP/TYPE pairs exist for every metric family.
  for (const name of [
    "ratelimit_requests_total",
    "ratelimit_allowed_total",
    "ratelimit_dropped_total",
    "ratelimit_throughput_per_second",
    "ratelimit_current_token_level",
    "ratelimit_wait_duration_seconds",
  ]) {
    assert.match(text, new RegExp(`^# TYPE ${name} (counter|gauge|histogram)$`, "m"));
  }

  const metrics = parseMetrics(text);
  assert.equal(metrics.get("ratelimit_allowed_total"), 4);
  assert.equal(metrics.get("ratelimit_dropped_total"), 2);
  assert.equal(
    metrics.get("ratelimit_requests_total"),
    metrics.get("ratelimit_allowed_total")! + metrics.get("ratelimit_dropped_total")!,
  );
  assert.equal(metrics.get("ratelimit_current_token_level"), 0);

  // Histogram invariants: cumulative buckets ending at +Inf === count.
  const bucketLines = text
    .split("\n")
    .filter((l) => l.startsWith("ratelimit_wait_duration_seconds_bucket"));
  const inf = bucketLines.find((l) => l.includes('le="+Inf"'))!;
  assert.ok(bucketLines.length >= 2);
  assert.equal(
    Number(inf.split(" ").pop()),
    metrics.get("ratelimit_wait_duration_seconds_count"),
  );
});

test("metrics interface stays consistent while sampled during a stress run", async () => {
  const limiter = new TokenBucketLimiter({
    capacity: 512,
    refillPerSec: 50_000,
    windowMs: 200,
  });

  // Drive load from a tight loop while sampling the metrics interface.
  let stop = false;
  let localAllowed = 0;
  let localDropped = 0;
  const driver = (async () => {
    while (!stop) {
      for (let i = 0; i < 1_000; i++) {
        if (limiter.tryAcquire()) localAllowed++;
        else localDropped++;
      }
      // Interleave blocking acquisitions to populate the wait histogram.
      await Promise.all(Array.from({ length: 8 }, () => limiter.acquire()));
      localAllowed += 8;
    }
  })();

  const samples: Map<string, number>[] = [];
  for (let i = 0; i < 10; i++) {
    const res = resolveMetricsResponse(limiter, "GET", "/metrics");
    assert.equal(res.status, 200);
    assert.equal(res.contentType, PROMETHEUS_CONTENT_TYPE);
    const parsed = parseMetrics(res.body);
    // Conservation must hold for every single sample taken mid-stress.
    assert.equal(
      parsed.get("ratelimit_requests_total"),
      parsed.get("ratelimit_allowed_total")! + parsed.get("ratelimit_dropped_total")!,
    );
    samples.push(parsed);
    await new Promise((r) => setTimeout(r, 25));
  }
  stop = true;
  await driver;

  // Counters are monotonic across samples.
  for (let i = 1; i < samples.length; i++) {
    assert.ok(
      samples[i].get("ratelimit_requests_total")! >=
        samples[i - 1].get("ratelimit_requests_total")!,
    );
  }
  // Load actually flowed and the histogram collected waits.
  const last = samples[samples.length - 1];
  assert.ok(last.get("ratelimit_allowed_total")! > 0);
  assert.ok(last.get("ratelimit_dropped_total")! > 0);
  assert.ok(last.get("ratelimit_wait_duration_seconds_count")! > 0);

  // Final JSON snapshot agrees with the local tally (no requests after stop).
  const res = resolveMetricsResponse(limiter, "GET", "/snapshot");
  const snap = JSON.parse(res.body);
  assert.equal(snap.allowedTotal, localAllowed);
  assert.equal(snap.droppedTotal, localDropped);
  assert.equal(snap.requestsTotal, localAllowed + localDropped);
  console.log(
    `    stress sample: allowed=${snap.allowedTotal} dropped=${snap.droppedTotal} ` +
      `windows=${snap.windows.length} waits=${snap.waitDuration.count}`,
  );
});

test("routing: unknown paths and non-GET methods return 404", () => {
  const limiter = new TokenBucketLimiter({ capacity: 1, refillPerSec: 1 });
  assert.equal(resolveMetricsResponse(limiter, "GET", "/nope").status, 404);
  assert.equal(resolveMetricsResponse(limiter, "POST", "/metrics").status, 404);
});

test("live HTTP server serves /metrics and /snapshot", async (t) => {
  const limiter = new TokenBucketLimiter({ capacity: 8, refillPerSec: 10 });
  for (let i = 0; i < 10; i++) limiter.tryAcquire();
  let server: Awaited<ReturnType<typeof startMetricsServer>>;
  try {
    server = await startMetricsServer(limiter);
  } catch (err) {
    // Sandboxed environments may forbid listening sockets entirely.
    if ((err as NodeJS.ErrnoException).code === "EPERM") {
      t.skip("listening sockets not permitted in this environment");
      return;
    }
    throw err;
  }
  try {
    const res = await fetch(`${server.url}/metrics`);
    assert.equal(res.status, 200);
    assert.match(res.headers.get("content-type")!, /text\/plain/);
    const parsed = parseMetrics(await res.text());
    assert.equal(parsed.get("ratelimit_allowed_total"), 8);
    assert.equal(parsed.get("ratelimit_dropped_total"), 2);

    const snap = await (await fetch(`${server.url}/snapshot`)).json();
    assert.equal(snap.requestsTotal, 10);

    assert.equal((await fetch(`${server.url}/nope`)).status, 404);
  } finally {
    await new Promise((r) => server.server.close(r));
  }
});
