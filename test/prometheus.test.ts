import { test } from "node:test";
import assert from "node:assert/strict";
import { RateLimiterMetrics } from "../src/metrics.ts";
import { renderPrometheus } from "../src/prometheus.ts";
import { FakeClock } from "./helpers.ts";

function sampleMetrics() {
  const clock = new FakeClock();
  const m = new RateLimiterMetrics(clock);
  m.recordAllowed(9);
  m.recordAllowed(8);
  m.recordDropped(0);
  m.recordWaitDuration(0.002);
  m.recordWaitDuration(0.2);
  clock.advance(1000);
  return m;
}

test("exposition contains HELP/TYPE and all four metric families", () => {
  const m = sampleMetrics();
  const text = renderPrometheus(m, m.snapshot());

  for (const name of [
    "ratelimit_allowed_total",
    "ratelimit_dropped_total",
    "ratelimit_wait_duration_seconds",
    "ratelimit_token_level",
    "ratelimit_window_allowed",
    "ratelimit_window_dropped",
    "ratelimit_window_throughput_per_sec",
  ]) {
    assert.match(text, new RegExp(`^# TYPE ${name} `, "m"), `missing TYPE for ${name}`);
    assert.match(text, new RegExp(`^# HELP ${name} `, "m"), `missing HELP for ${name}`);
  }

  assert.match(text, /^ratelimit_allowed_total 2$/m);
  assert.match(text, /^ratelimit_dropped_total 1$/m);
  assert.match(text, /^ratelimit_token_level 0$/m);
  assert.match(text, /^ratelimit_window_allowed 2$/m);
  assert.match(text, /^ratelimit_window_dropped 1$/m);
  assert.match(text, /^ratelimit_window_throughput_per_sec 2$/m);
});

test("histogram exposition is cumulative and terminates with +Inf", () => {
  const m = sampleMetrics();
  const text = renderPrometheus(m, m.snapshot());

  const bucketLines = text
    .split("\n")
    .filter((l) => l.startsWith("ratelimit_wait_duration_seconds_bucket"));
  assert.ok(bucketLines.length > 2);
  // Cumulative counts must be non-decreasing as le grows.
  const counts = bucketLines.map((l) => Number(l.split(" ").pop()));
  for (let i = 1; i < counts.length; i++) {
    assert.ok(counts[i] >= counts[i - 1], `buckets not cumulative: ${counts}`);
  }
  assert.match(text, /ratelimit_wait_duration_seconds_bucket\{le="\+Inf"\} 2/);
  assert.match(text, /^ratelimit_wait_duration_seconds_count 2$/m);
  assert.match(text, /^ratelimit_wait_duration_seconds_sum 0.202$/m);
  // Both observations fall in buckets >= 0.2; the 0.002 one not in le="0.001".
  assert.match(text, /ratelimit_wait_duration_seconds_bucket\{le="0.001"\} 0/);
  assert.match(text, /ratelimit_wait_duration_seconds_bucket\{le="0.005"\} 1/);
});

test("custom prefix is applied", () => {
  const m = sampleMetrics();
  const text = renderPrometheus(m, m.snapshot(), { prefix: "myapp_rl" });
  assert.match(text, /^myapp_rl_allowed_total 2$/m);
  assert.doesNotMatch(text, /^ratelimit_/m);
});
