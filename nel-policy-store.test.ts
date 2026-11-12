import { test } from "node:test";
import assert from "node:assert/strict";
import { NelPolicyStore } from "./nel-policy-store.ts";

const REPORT_TO = JSON.stringify({
  default: {
    max_age: 3600,
    endpoints: [{ url: "https://collector.example.com/nel" }],
  },
});

const OTHER_REPORT_TO = JSON.stringify({
  attacker: {
    max_age: 3600,
    endpoints: [{ url: "https://evil.example.com/collect" }],
  },
});

function nelHeader(extra: Record<string, unknown> = {}): string {
  return JSON.stringify({ report_to: "default", max_age: 3600, ...extra });
}

function makeStore(random = () => 0): NelPolicyStore {
  return new NelPolicyStore({ random });
}

test("accepts a valid NEL policy over HTTPS and reports to its own endpoints", () => {
  const store = makeStore();
  store.processResponseHeaders("https://example.com/page", {
    "Report-To": REPORT_TO,
    NEL: nelHeader(),
  });
  const policy = store.getPolicy("https://example.com");
  assert.ok(policy);
  const report = store.generateReport({
    url: "https://example.com/missing",
    errorType: "http.error",
    statusCode: 404,
  });
  assert.ok(report);
  assert.equal(report.url, "https://example.com/missing");
  assert.equal(report.phase, "error");
  assert.deepEqual(report.deliveryEndpoints, [
    "https://collector.example.com/nel",
  ]);
});

test("ignores NEL headers injected over cleartext HTTP", () => {
  const store = makeStore();
  store.processResponseHeaders("http://example.com/page", {
    "Report-To": OTHER_REPORT_TO,
    NEL: JSON.stringify({ report_to: "attacker", max_age: 3600 }),
  });
  assert.equal(store.getPolicy("http://example.com"), undefined);
  assert.equal(store.getPolicy("https://example.com"), undefined);
  assert.equal(
    store.generateReport({ url: "http://example.com/x", errorType: "dns.failed" }),
    null,
  );
});

test("ignores NEL headers from IP-literal hosts", () => {
  const store = makeStore();
  for (const url of [
    "https://192.168.0.1/page",
    "https://[2001:db8::1]/page",
    "https://127.0.0.1:8443/page",
  ]) {
    store.processResponseHeaders(url, {
      "Report-To": OTHER_REPORT_TO,
      NEL: JSON.stringify({ report_to: "attacker", max_age: 3600 }),
    });
  }
  assert.equal(store.getPolicy("https://192.168.0.1"), undefined);
  assert.equal(store.getPolicy("https://[2001:db8::1]"), undefined);
  assert.equal(store.getPolicy("https://127.0.0.1:8443"), undefined);
});

test("rejects report_to groups not declared in the same response", () => {
  const store = makeStore();
  // NEL references a group this response never declared.
  store.processResponseHeaders("https://example.com/", {
    "Report-To": OTHER_REPORT_TO,
    NEL: nelHeader(), // references "default", only "attacker" declared
  });
  assert.equal(store.getPolicy("https://example.com"), undefined);

  // NEL with no Report-To header at all.
  store.processResponseHeaders("https://example.org/", {
    NEL: nelHeader(),
  });
  assert.equal(store.getPolicy("https://example.org"), undefined);
});

test("out-of-range fractions invalidate the whole policy", () => {
  const store = makeStore();
  for (const fraction of [-0.1, 1.5, Number.NaN, "0.5"]) {
    store.processResponseHeaders("https://example.com/", {
      "Report-To": REPORT_TO,
      NEL: nelHeader({ success_fraction: fraction }),
    });
    assert.equal(
      store.getPolicy("https://example.com"),
      undefined,
      `success_fraction=${String(fraction)} must be rejected`,
    );
    store.processResponseHeaders("https://example.com/", {
      "Report-To": REPORT_TO,
      NEL: nelHeader({ failure_fraction: fraction }),
    });
    assert.equal(
      store.getPolicy("https://example.com"),
      undefined,
      `failure_fraction=${String(fraction)} must be rejected`,
    );
  }
  // Boundary values 0 and 1 are valid.
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader({ success_fraction: 0, failure_fraction: 1 }),
  });
  assert.ok(store.getPolicy("https://example.com"));
});

test("max_age of 0 deletes the existing policy for that origin", () => {
  const store = makeStore();
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader(),
  });
  assert.ok(store.getPolicy("https://example.com"));
  store.processResponseHeaders("https://example.com/", {
    NEL: JSON.stringify({ report_to: "default", max_age: 0 }),
  });
  assert.equal(store.getPolicy("https://example.com"), undefined);
});

test("subdomain policy does not affect the parent domain", () => {
  const store = makeStore();
  // Attacker-controlled subdomain sets a policy, even with include_subdomains.
  store.processResponseHeaders("https://evil.example.com/", {
    "Report-To": OTHER_REPORT_TO,
    NEL: JSON.stringify({
      report_to: "attacker",
      max_age: 3600,
      include_subdomains: true,
    }),
  });
  assert.equal(
    store.generateReport({ url: "https://example.com/api", errorType: "tcp.failed" }),
    null,
    "parent domain requests must not match a subdomain's policy",
  );
  assert.equal(
    store.generateReport({
      url: "https://sibling.example.com/api",
      errorType: "tcp.failed",
    }),
    null,
    "sibling subdomains must not match either",
  );
});

test("include_subdomains matches subdomain requests and strips report url to origin", () => {
  const store = makeStore();
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader({ include_subdomains: true }),
  });
  const report = store.generateReport({
    url: "https://cdn.example.com/assets/secret-token.js?session=abc",
    errorType: "http.error",
    statusCode: 500,
  });
  assert.ok(report);
  assert.equal(report.url, "https://cdn.example.com");
  assert.deepEqual(report.deliveryEndpoints, [
    "https://collector.example.com/nel",
  ]);
});

test("without include_subdomains, subdomain requests are not covered", () => {
  const store = makeStore();
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader(),
  });
  assert.equal(
    store.generateReport({
      url: "https://cdn.example.com/x",
      errorType: "dns.failed",
    }),
    null,
  );
});

test("exact-origin policy wins over an ancestor include_subdomains policy", () => {
  const store = makeStore();
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader({ include_subdomains: true }),
  });
  store.processResponseHeaders("https://cdn.example.com/", {
    "Report-To": OTHER_REPORT_TO,
    NEL: JSON.stringify({ report_to: "attacker", max_age: 3600 }),
  });
  const report = store.generateReport({
    url: "https://cdn.example.com/file",
    errorType: "http.error",
  });
  assert.ok(report);
  assert.equal(report.url, "https://cdn.example.com/file");
  assert.deepEqual(report.deliveryEndpoints, [
    "https://evil.example.com/collect",
  ]);
});

test("reports only go to the policy's own Report-To endpoints", () => {
  const store = makeStore();
  store.processResponseHeaders("https://a.example/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader(),
  });
  store.processResponseHeaders("https://b.example/", {
    "Report-To": OTHER_REPORT_TO,
    NEL: JSON.stringify({ report_to: "attacker", max_age: 3600 }),
  });
  const reportA = store.generateReport({
    url: "https://a.example/x",
    errorType: "http.error",
  });
  const reportB = store.generateReport({
    url: "https://b.example/x",
    errorType: "http.error",
  });
  assert.deepEqual(reportA?.deliveryEndpoints, [
    "https://collector.example.com/nel",
  ]);
  assert.deepEqual(reportB?.deliveryEndpoints, [
    "https://evil.example.com/collect",
  ]);
});

test("sampling respects success_fraction and failure_fraction", () => {
  const store = new NelPolicyStore({ random: () => 0.4 });
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader({ success_fraction: 0.5, failure_fraction: 0.3 }),
  });
  assert.ok(
    store.generateReport({ url: "https://example.com/ok", statusCode: 200 }),
    "0.4 < 0.5 keeps the success report",
  );
  assert.equal(
    store.generateReport({ url: "https://example.com/err", errorType: "http.error" }),
    null,
    "0.4 >= 0.3 drops the failure report",
  );
  // fraction 0 never reports.
  const zero = new NelPolicyStore({ random: () => 0 });
  zero.processResponseHeaders("https://z.example/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader({ success_fraction: 0 }),
  });
  assert.equal(
    zero.generateReport({ url: "https://z.example/ok", statusCode: 200 }),
    null,
  );
});

test("policies expire after max_age", () => {
  let now = 1_000_000;
  const store = new NelPolicyStore({ now: () => now, random: () => 0 });
  store.processResponseHeaders("https://example.com/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader({ max_age: 10 }),
  });
  assert.ok(store.getPolicy("https://example.com"));
  now += 10_001;
  assert.equal(store.getPolicy("https://example.com"), undefined);
  assert.equal(
    store.generateReport({ url: "https://example.com/x", errorType: "http.error" }),
    null,
  );
});

test("malformed NEL JSON and non-object values are ignored", () => {
  const store = makeStore();
  for (const bad of ["not json", "[1,2]", '"str"', "42", "null"]) {
    store.processResponseHeaders("https://example.com/", {
      "Report-To": REPORT_TO,
      NEL: bad,
    });
  }
  assert.equal(store.getPolicy("https://example.com"), undefined);
});

test("origins are strictly separated by scheme and port", () => {
  const store = makeStore();
  store.processResponseHeaders("https://example.com:8443/", {
    "Report-To": REPORT_TO,
    NEL: nelHeader(),
  });
  assert.equal(store.getPolicy("https://example.com"), undefined);
  assert.ok(store.getPolicy("https://example.com:8443"));
});
