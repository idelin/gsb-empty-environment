import { test } from "node:test";
import assert from "node:assert/strict";
import { deliverReports } from "../src/delivery.ts";
import type { QueuedReport } from "../src/delivery.ts";

interface CapturedCall {
  url: string;
  init: RequestInit;
}

function makeFetch(calls: CapturedCall[], status = 200): typeof fetch {
  return (async (input: unknown, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} });
    return new Response(null, { status });
  }) as typeof fetch;
}

function makeReport(endpoint: string, n: number): QueuedReport {
  return {
    type: "csp-violation",
    url: "https://site.example/page",
    endpoint,
    body: { seq: n },
  };
}

const ENDPOINTS = new Map([
  ["default", "https://reports.example.com/r"],
  ["csp", "https://csp.example.com/r"],
]);

test("posts a JSON array with application/reports+json and no credentials", async () => {
  const calls: CapturedCall[] = [];
  const attempts = await deliverReports({
    endpoints: ENDPOINTS,
    reports: [makeReport("default", 1)],
    fetchImpl: makeFetch(calls),
  });
  assert.equal(attempts.length, 1);
  assert.equal(attempts[0].ok, true);
  assert.equal(calls.length, 1);

  const { url, init } = calls[0];
  assert.equal(url, "https://reports.example.com/r");
  assert.equal(init.method, "POST");
  assert.equal(init.credentials, "omit");

  const headers = new Headers(init.headers);
  assert.equal(headers.get("content-type"), "application/reports+json");
  assert.equal(headers.get("cookie"), null);
  assert.equal(headers.get("authorization"), null);
  assert.equal([...headers.keys()].length, 1);

  const payload = JSON.parse(String(init.body));
  assert.deepEqual(payload, [
    {
      type: "csp-violation",
      url: "https://site.example/page",
      body: { seq: 1 },
    },
  ]);
});

test("drops reports referencing undeclared endpoints", async () => {
  const calls: CapturedCall[] = [];
  const attempts = await deliverReports({
    endpoints: ENDPOINTS,
    reports: [makeReport("attacker-controlled", 1), makeReport("default", 2)],
    fetchImpl: makeFetch(calls),
  });
  assert.equal(calls.length, 1);
  assert.equal(attempts.length, 1);
  assert.equal(attempts[0].endpoint, "default");
  const payload = JSON.parse(String(calls[0].init.body));
  assert.equal(payload.length, 1);
  assert.deepEqual(payload[0].body, { seq: 2 });
});

test("batches deliveries at 100 reports per request", async () => {
  const calls: CapturedCall[] = [];
  const reports = Array.from({ length: 250 }, (_, i) => makeReport("default", i));
  const attempts = await deliverReports({
    endpoints: ENDPOINTS,
    reports,
    fetchImpl: makeFetch(calls),
  });
  assert.equal(attempts.length, 3);
  assert.deepEqual(
    attempts.map((a) => a.reportCount),
    [100, 100, 50],
  );
  for (const call of calls) {
    const payload = JSON.parse(String(call.init.body));
    assert.ok(payload.length <= 100);
  }
});

test("groups reports by endpoint", async () => {
  const calls: CapturedCall[] = [];
  await deliverReports({
    endpoints: ENDPOINTS,
    reports: [makeReport("default", 1), makeReport("csp", 2), makeReport("default", 3)],
    fetchImpl: makeFetch(calls),
  });
  assert.equal(calls.length, 2);
  const byUrl = new Map(calls.map((c) => [c.url, c]));
  const defaultPayload = JSON.parse(String(byUrl.get("https://reports.example.com/r")!.init.body));
  const cspPayload = JSON.parse(String(byUrl.get("https://csp.example.com/r")!.init.body));
  assert.deepEqual(defaultPayload.map((r: { body: { seq: number } }) => r.body.seq), [1, 3]);
  assert.deepEqual(cspPayload.map((r: { body: { seq: number } }) => r.body.seq), [2]);
});

test("records failures without throwing", async () => {
  const failingFetch = (async () => {
    throw new Error("network down");
  }) as typeof fetch;
  const attempts = await deliverReports({
    endpoints: ENDPOINTS,
    reports: [makeReport("default", 1)],
    fetchImpl: failingFetch,
  });
  assert.equal(attempts.length, 1);
  assert.equal(attempts[0].ok, false);
  assert.ok(attempts[0].error instanceof Error);
});
