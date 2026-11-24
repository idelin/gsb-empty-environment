import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ReportDeliveryService,
  type OutgoingRequest,
  type Report,
  type Resolver,
  type Transport,
  type TransportResponse,
} from "../src/report-delivery.ts";

const PUBLIC_IP = "93.184.216.34";

function makeReports(n: number, type = "csp-violation"): Report[] {
  return Array.from({ length: n }, (_, i) => ({
    type,
    url: "https://site.example/page",
    body: { index: i },
  }));
}

function recorder(
  handler: (req: OutgoingRequest, n: number) => TransportResponse | Promise<TransportResponse>,
): { transport: Transport; requests: OutgoingRequest[] } {
  const requests: OutgoingRequest[] = [];
  const transport: Transport = async (req) => {
    requests.push(req);
    return handler(req, requests.length);
  };
  return { transport, requests };
}

const publicResolver: Resolver = async () => [PUBLIC_IP];
const ok = (): TransportResponse => ({ status: 200 });

function service(overrides: {
  documentUrl?: string;
  resolver?: Resolver;
  transport: Transport;
}): ReportDeliveryService {
  return new ReportDeliveryService({
    documentUrl: overrides.documentUrl ?? "https://site.example/page",
    resolver: overrides.resolver ?? publicResolver,
    transport: overrides.transport,
  });
}

test("delivers reports with correct content type and no credentials", async () => {
  const { transport, requests } = recorder(ok);
  const svc = service({ transport });
  svc.setEndpoints(['default="https://reports.example/collect"']);
  svc.enqueue("https://site.example", "default", makeReports(3));
  const [result] = await svc.flush();
  assert.equal(result.status, "delivered");
  assert.equal(result.delivered, 3);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].method, "POST");
  assert.deepEqual(requests[0].headers, { "content-type": "application/reports+json" });
  const headerNames = Object.keys(requests[0].headers).map((h) => h.toLowerCase());
  assert.ok(!headerNames.includes("cookie"));
  assert.ok(!headerNames.includes("authorization"));
  assert.equal(JSON.parse(requests[0].body).length, 3);
});

test("redirect to an intranet address is blocked before the hop is sent", async () => {
  const { transport, requests } = recorder(() => ({
    status: 302,
    headers: { location: "https://10.0.0.1/collect" },
  }));
  const svc = service({ transport });
  svc.setEndpoints(['default="https://reports.example/collect"']);
  svc.enqueue("https://site.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "blocked");
  assert.match(result.reason ?? "", /private/);
  assert.equal(requests.length, 1); // only the first, public hop was sent
});

test("redirect target is re-resolved: DNS rebinding on the second hop is blocked", async () => {
  const resolver: Resolver = async (host) =>
    host === "rebind.example" ? ["192.168.1.1"] : [PUBLIC_IP];
  const { transport, requests } = recorder((req) =>
    req.url.includes("reports.example")
      ? { status: 301, headers: { location: "https://rebind.example/collect" } }
      : ok(),
  );
  const svc = service({ transport, resolver });
  svc.setEndpoints(['default="https://reports.example/collect"']);
  svc.enqueue("https://site.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "blocked");
  assert.equal(requests.length, 1);
});

test("host resolving to multiple IPs with one private address is blocked (DNS rebinding)", async () => {
  const resolver: Resolver = async () => [PUBLIC_IP, "127.0.0.1"];
  const { transport, requests } = recorder(ok);
  const svc = service({ transport, resolver });
  svc.setEndpoints(['default="https://reports.example/collect"']);
  svc.enqueue("https://site.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "blocked");
  assert.equal(requests.length, 0);
});

test("integer and obfuscated IP endpoints are blocked", async () => {
  for (const endpoint of [
    "https://2130706433/collect", // decimal integer loopback
    "https://0x7f000001/collect", // hex integer loopback
    "https://0177.0.0.1/collect", // octal loopback
    "https://127.1/collect", // shortened loopback
    "https://[::ffff:127.0.0.1]/collect", // IPv4-mapped IPv6 loopback
    "https://[::1]/collect",
    "https://169.254.1.1/collect", // link-local
  ]) {
    const { transport, requests } = recorder(ok);
    const svc = service({ transport });
    svc.setEndpoints([`default="${endpoint}"`]);
    svc.enqueue("https://site.example", "default", makeReports(1));
    const [result] = await svc.flush();
    assert.equal(result.status, "blocked", endpoint);
    assert.equal(requests.length, 0, endpoint);
  }
});

test("document on a private address may deliver to the same address space", async () => {
  const resolver: Resolver = async (host) =>
    host === "intranet.example" ? ["10.0.0.5"] : ["10.0.0.6"];
  const { transport, requests } = recorder(ok);
  const svc = service({
    documentUrl: "https://intranet.example/app",
    resolver,
    transport,
  });
  svc.setEndpoints(['default="https://reports.internal/collect"']);
  svc.enqueue("https://intranet.example", "default", makeReports(2));
  const [result] = await svc.flush();
  assert.equal(result.status, "delivered");
  assert.equal(requests.length, 1);
});

test("private document may not deliver across address spaces", async () => {
  const resolver: Resolver = async () => ["10.0.0.5"];
  const { transport, requests } = recorder(ok);
  const svc = service({
    documentUrl: "https://intranet.example/app",
    resolver,
    transport,
  });
  svc.setEndpoints(['default="https://127.0.0.1/collect"']);
  svc.enqueue("https://intranet.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "blocked");
  assert.equal(requests.length, 0);
});

test("overflow beyond 100 reports is dropped per origin, not globally", async () => {
  const { transport, requests } = recorder(ok);
  const svc = service({ transport });
  svc.setEndpoints(['default="https://reports.example/collect"']);
  svc.enqueue("https://a.example", "default", makeReports(150));
  svc.enqueue("https://b.example", "default", makeReports(50));
  const results = await svc.flush();
  assert.equal(results.length, 2);
  const [a, b] = results;
  assert.equal(a.origin, "https://a.example");
  assert.equal(a.delivered, 100);
  assert.equal(a.dropped, 50);
  assert.equal(b.origin, "https://b.example");
  assert.equal(b.delivered, 50);
  assert.equal(b.dropped, 0);
  assert.equal(JSON.parse(requests[0].body).length, 100);
  assert.equal(JSON.parse(requests[1].body).length, 50);
  // Queues are drained; a second flush sends nothing.
  assert.deepEqual(await svc.flush(), []);
});

test("conflicting duplicate endpoint keys: first header wins", async () => {
  const { transport, requests } = recorder(ok);
  const svc = service({ transport });
  svc.setEndpoints([
    'default="https://first.example/collect"',
    'default="https://second.example/collect"',
  ]);
  svc.enqueue("https://site.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "delivered");
  assert.equal(requests[0].url, "https://first.example/collect");
});

test("no credentials are sent on any redirect hop", async () => {
  const resolver: Resolver = async () => [PUBLIC_IP];
  const { transport, requests } = recorder((req) =>
    req.url.includes("one.example")
      ? { status: 302, headers: { location: "https://two.example/collect" } }
      : ok(),
  );
  const svc = service({ transport, resolver });
  svc.setEndpoints(['default="https://one.example/collect"']);
  svc.enqueue("https://site.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "delivered");
  assert.equal(requests.length, 2);
  for (const req of requests) {
    const names = Object.keys(req.headers).map((h) => h.toLowerCase());
    assert.ok(!names.includes("cookie"));
    assert.ok(!names.includes("authorization"));
    assert.deepEqual(names, ["content-type"]);
  }
});

test("non-https redirect targets are blocked", async () => {
  const { transport, requests } = recorder(() => ({
    status: 302,
    headers: { location: "http://reports.example/collect" },
  }));
  const svc = service({ transport });
  svc.setEndpoints(['default="https://reports.example/collect"']);
  svc.enqueue("https://site.example", "default", makeReports(1));
  const [result] = await svc.flush();
  assert.equal(result.status, "blocked");
  assert.equal(requests.length, 1);
});
