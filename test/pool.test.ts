import { test } from "node:test";
import assert from "node:assert/strict";
import { ConnectionPool, ConnectError } from "../src/pool.ts";
import type { TenantPolicyConfig } from "../src/policy.ts";
import type { Resolution } from "../src/resolver.ts";
import { FakeConnection, FakeDialer, StaticResolver, flush } from "./fakes.ts";

const PORT = 443;
const START = 1_000_000;

function allowConfig(cidrs: string[]): TenantPolicyConfig {
  return {
    rules: cidrs.map(cidr => ({ cidr, action: "allow" as const })),
    allowedSuffixes: ["example.com"],
  };
}

function single(addresses: string[], chainTtlSec = 60, addrTtlSec = 60): Resolution {
  return {
    chain: [{ name: "www.example.com", ttlSec: chainTtlSec }],
    addresses: addresses.map(address => ({ address, ttlSec: addrTtlSec })),
  };
}

function makePool(connectTimeoutMs = 50) {
  const resolver = new StaticResolver();
  const dialer = new FakeDialer();
  let now = START;
  const pool = new ConnectionPool({
    resolver,
    dialer,
    connectTimeoutMs,
    nowMs: () => now,
  });
  return {
    pool,
    resolver,
    dialer,
    setNow(value: number) {
      now = value;
    },
  };
}

test("connects and reuses the pooled connection for the same name", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  const pending = pool.connect("tenant-a", "WWW.Example.Com.", PORT);
  await flush();
  assert.deepEqual(dialer.attempts, [{ address: "203.0.113.10", port: PORT }]);
  const conn = dialer.succeed("203.0.113.10");
  assert.equal(await pending, conn);
  const again = await pool.connect("tenant-a", "www.example.com", PORT);
  assert.equal(again, conn);
  assert.equal(dialer.attempts.length, 1);
  assert.equal(resolver.queries.length, 1);
});

test("no substitution across tenants or hostnames sharing an address", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  pool.setPolicy("tenant-b", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  resolver.add("api.example.com", {
    chain: [{ name: "api.example.com", ttlSec: 60 }],
    addresses: [{ address: "203.0.113.10", ttlSec: 60 }],
  });
  const p1 = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  const connA = dialer.succeed("203.0.113.10");
  assert.equal(await p1, connA);
  const p2 = pool.connect("tenant-b", "www.example.com", PORT);
  await flush();
  const connB = dialer.succeed("203.0.113.10");
  assert.equal(await p2, connB);
  const p3 = pool.connect("tenant-a", "api.example.com", PORT);
  await flush();
  const connC = dialer.succeed("203.0.113.10");
  assert.equal(await p3, connC);
  assert.equal(dialer.attempts.length, 3);
  assert.ok(connA !== connB && connB !== connC && connA !== connC);
  assert.equal(pool.connectionCount, 3);
});

test("dials ipv4-mapped results as plain ipv4", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["::ffff:203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  assert.deepEqual(dialer.attempts, [{ address: "203.0.113.10", port: PORT }]);
  const conn = dialer.succeed("203.0.113.10");
  assert.equal(await pending, conn);
});

test("races dual-stack candidates, first success wins, loser closed", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["2001:db8::/32", "203.0.113.0/24"]));
  resolver.add("www.example.com", single(["2001:db8::10", "203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  assert.equal(dialer.attempts.length, 2);
  const connV6 = dialer.succeed("2001:db8::10");
  const connV4 = dialer.succeed("203.0.113.10");
  assert.equal(await pending, connV6);
  assert.equal(connV6.closed, false);
  assert.equal(connV4.closed, true);
  assert.equal(pool.connectionCount, 1);
});

test("late loser connection is closed after the race already ended", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["2001:db8::/32", "203.0.113.0/24"]));
  resolver.add("www.example.com", single(["2001:db8::10", "203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  const connV4 = dialer.succeed("203.0.113.10");
  assert.equal(await pending, connV4);
  const lateV6 = dialer.succeed("2001:db8::10");
  await flush();
  assert.equal(lateV6.closed, true);
  assert.equal(connV4.closed, false);
});

test("revoked candidate succeeding concurrently is closed, survivor wins", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10", "203.0.113.11"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  assert.equal(dialer.attempts.length, 2);
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.10/32"]));
  const connRevoked = dialer.succeed("203.0.113.11");
  const connAllowed = dialer.succeed("203.0.113.10");
  assert.equal(await pending, connAllowed);
  assert.equal(connRevoked.closed, true);
  assert.equal(connAllowed.closed, false);
  assert.equal(pool.connectionCount, 1);
});

test("connect rejects when every in-flight candidate loses authorization", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  pool.setPolicy("tenant-a", allowConfig(["10.0.0.0/8"]));
  const conn = dialer.succeed("203.0.113.10");
  await assert.rejects(pending, ConnectError);
  assert.equal(conn.closed, true);
  assert.equal(pool.connectionCount, 0);
});

test("handshake timeout rejects and reaps late connections", async () => {
  const { pool, resolver, dialer } = makePool(30);
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  await assert.rejects(pending, /timed out/);
  const late = dialer.succeed("203.0.113.10");
  await flush();
  assert.equal(late.closed, true);
  assert.equal(pool.connectionCount, 0);
});

test("credential expires at the earliest cname hop ttl", async () => {
  const { pool, resolver, dialer, setNow } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", {
    chain: [
      { name: "www.example.com", ttlSec: 60 },
      { name: "edge.example.com", ttlSec: 5 },
    ],
    addresses: [{ address: "203.0.113.10", ttlSec: 120 }],
  });
  const p1 = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  const conn1 = dialer.succeed("203.0.113.10");
  assert.equal(await p1, conn1);
  setNow(START + 6_000);
  const p2 = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  assert.equal(conn1.closed, true);
  assert.equal(dialer.attempts.length, 2);
  assert.equal(resolver.queries.length, 2);
  const conn2 = dialer.succeed("203.0.113.10");
  assert.equal(await p2, conn2);
  assert.notEqual(conn2, conn1);
});

test("policy update evicts cached connections that lost authorization", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  const conn = dialer.succeed("203.0.113.10");
  await pending;
  assert.equal(pool.connectionCount, 1);
  pool.setPolicy("tenant-a", allowConfig(["10.0.0.0/8"]));
  assert.equal(conn.closed, true);
  assert.equal(pool.connectionCount, 0);
});

test("reuse verifies the actual peer of the pooled connection", async () => {
  const { pool, resolver, dialer } = makePool();
  pool.setPolicy("tenant-a", allowConfig(["203.0.113.0/24"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  const pending = pool.connect("tenant-a", "www.example.com", PORT);
  await flush();
  const stranger = new FakeConnection("198.51.100.99", PORT);
  dialer.pending[0].resolve(stranger);
  await assert.rejects(pending, ConnectError);
  assert.equal(stranger.closed, true);
  assert.equal(pool.connectionCount, 0);
});

test("fails closed when the tenant has no policy or no authorized address", async () => {
  const { pool, resolver } = makePool();
  await assert.rejects(pool.connect("ghost", "www.example.com", PORT), ConnectError);
  pool.setPolicy("tenant-a", allowConfig(["10.0.0.0/8"]));
  resolver.add("www.example.com", single(["203.0.113.10"]));
  await assert.rejects(pool.connect("tenant-a", "www.example.com", PORT), /no authorized address/);
});
