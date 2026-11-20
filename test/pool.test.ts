import { test } from "node:test";
import assert from "node:assert/strict";
import { WebTransportSessionPool } from "../src/pool.ts";
import type { ConnectionKeyParts, SessionHandle } from "../src/types.ts";
import { canonicalizeKey } from "../src/types.ts";

const NOW = Date.now();

function makeParts(overrides: Partial<ConnectionKeyParts> = {}): ConnectionKeyParts {
  return {
    partitionKey: { topLevelSite: "https://example.com" },
    proxyChain: [],
    cert: {
      chainFingerprint: "sha256:aaa",
      verifiedNames: ["a.example.com", "b.example.com", "*.cdn.example.com"],
      validUntil: NOW + 86_400_000,
    },
    ech: { kind: "none" },
    alpn: "h3",
    remote: { ip: "192.0.2.1", port: 443 },
    ...overrides,
  };
}

function openSession(
  pool: WebTransportSessionPool,
  parts: ConnectionKeyParts,
  origin: string,
  extra: { requestedSessionId?: number } = {},
) {
  return pool.createSession(
    parts,
    { origin, url: `${origin}/wt`, offeredVersions: ["1"], ...extra },
    "draft-ietf-webtrans-http3-02",
    "1",
    { maxStreams: 8, maxDatagrams: 16 },
  );
}

function mustOpen(
  pool: WebTransportSessionPool,
  parts: ConnectionKeyParts,
  origin: string,
  extra: { requestedSessionId?: number } = {},
): SessionHandle {
  const res = openSession(pool, parts, origin, extra);
  assert.ok(res.ok, `expected session for ${origin} to open, got: ${JSON.stringify(res)}`);
  return res.handle;
}

// 1. Two origins share a certificate but pooling is not authorization.
test("same cert, two origins: second origin rejected without ORIGIN frame", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();

  const a = mustOpen(pool, parts, "https://a.example.com");
  const denied = openSession(pool, parts, "https://b.example.com");
  assert.equal(denied.ok, false);
  if (!denied.ok) {
    assert.equal(denied.reason, "no-same-origin-session-and-no-origin-frame");
  }

  // Same-origin request on the same connection is fine and pooled.
  const a2 = openSession(pool, parts, "https://a.example.com");
  assert.ok(a2.ok && a2.pooled);
  assert.notEqual(a.session.id, a2.ok ? a2.handle.session.id : -1);
});

// 2. A forged ORIGIN frame must never authorize co-tenancy.
test("forged/unvalidated ORIGIN frame is ignored", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();
  mustOpen(pool, parts, "https://a.example.com");

  const conn = pool.getOrCreateConnection(parts);
  // Attacker-injected frame: not validated by the HTTP/3 layer.
  conn.originFrame = { validated: false, origins: ["https://b.example.com"] };
  const denied = openSession(pool, parts, "https://b.example.com");
  assert.equal(denied.ok, false);

  // Even a validated frame cannot stretch beyond certificate coverage.
  conn.originFrame = { validated: true, origins: ["https://evil.other.com"] };
  const uncovered = openSession(pool, parts, "https://evil.other.com");
  assert.equal(uncovered.ok, false);
  if (!uncovered.ok) assert.equal(uncovered.reason, "cert-does-not-cover-origin");
});

// 3. Session IDs are pool-allocated, never reused, never caller-chosen.
test("session ID reuse: caller IDs ignored, closed/unknown datagrams dropped", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();

  const victim = mustOpen(pool, parts, "https://a.example.com");
  // Malicious co-tenant (same origin here) tries to squat on victim's ID.
  const attacker = mustOpen(pool, parts, "https://a.example.com", {
    requestedSessionId: victim.session.id,
  });
  assert.notEqual(attacker.session.id, victim.session.id);
  assert.ok(attacker.session.id > victim.session.id, "IDs are monotonic");

  const gen = pool.getOrCreateConnection(parts).generation;
  const payload = new Uint8Array([1, 2, 3]);

  assert.equal(pool.routeDatagram(gen, victim.session.id, payload), "delivered");
  assert.equal(victim.session.datagramQueue.length, 1);
  assert.equal(attacker.session.datagramQueue.length, 0, "no cross-session leakage");

  pool.closeSession(victim);
  assert.equal(pool.routeDatagram(gen, victim.session.id, payload), "dropped-closed-session");
  assert.equal(pool.routeDatagram(gen, 9999, payload), "dropped-unknown-session");
  assert.equal(pool.drops.closedSession, 1);
  assert.equal(pool.drops.unknownSession, 1);
});

// 4. Network switch freezes the old generation and closes its sessions.
test("network change: old generation frozen, late datagrams dropped", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();
  const s = mustOpen(pool, parts, "https://a.example.com");
  const oldGen = pool.getOrCreateConnection(parts).generation;

  pool.freeze(parts, "network-change");

  // Late datagram for the old generation's (now closed) session is dropped.
  assert.equal(
    pool.routeDatagram(oldGen, s.session.id, new Uint8Array([9])),
    "dropped-closed-session",
  );

  // New allocation lands on a fresh connection with a bumped generation.
  const s2 = mustOpen(pool, parts, "https://a.example.com");
  const newConn = pool.getOrCreateConnection(parts);
  assert.ok(newConn.generation > oldGen);
  assert.equal(newConn.state, "active");
  assert.equal(
    pool.routeDatagram(newConn.generation, s2.session.id, new Uint8Array([1])),
    "delivered",
  );
});

// 5. GOAWAY race: draining connection serves existing sessions only.
test("GOAWAY freezes new allocation but keeps existing sessions alive", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();
  const s1 = mustOpen(pool, parts, "https://a.example.com");
  const oldGen = pool.getOrCreateConnection(parts).generation;

  pool.freeze(parts, "goaway");
  // The old connection is draining; the pool hands out a fresh generation.
  const afterGoaway = pool.getOrCreateConnection(parts);
  assert.equal(afterGoaway.state, "active");
  assert.ok(afterGoaway.generation > oldGen);

  // Existing session still receives datagrams on the draining connection.
  assert.equal(
    pool.routeDatagram(oldGen, s1.session.id, new Uint8Array([7])),
    "delivered",
  );

  // A racing new session must not land on the draining connection.
  const s2 = mustOpen(pool, parts, "https://a.example.com");
  const newGen = pool.getOrCreateConnection(parts).generation;
  assert.ok(newGen > oldGen, "new session allocated on a fresh generation");
  // Session IDs are connection-scoped; (generation, id) is the routing key,
  // so identical IDs on different generations never collide.
  assert.equal(
    pool.routeDatagram(newGen, s2.session.id, new Uint8Array([8])),
    "delivered",
  );
  assert.equal(s2.session.datagramQueue.length, 1);
  assert.equal(s1.session.datagramQueue.length, 1, "no cross-generation leakage");
});

// 5b. 0-RTT rejection and cert updates also freeze allocation.
test("0-RTT reject and cert update freeze new allocations", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();
  mustOpen(pool, parts, "https://a.example.com");

  pool.freeze(parts, "zero-rtt-rejected");
  const connAfter0rtt = pool.getOrCreateConnection(parts);
  assert.equal(connAfter0rtt.state, "active");
  assert.equal(connAfter0rtt.generation, 2);

  const pool2 = new WebTransportSessionPool();
  const parts2 = makeParts();
  mustOpen(pool2, parts2, "https://a.example.com");
  pool2.freeze(parts2, "cert-update");
  assert.equal(pool2.getOrCreateConnection(parts2).generation, 2);
});

// 6. Legitimate authorized co-tenancy: pooled, but state stays isolated.
test("validated ORIGIN frame allows co-tenancy with strict state isolation", () => {
  const pool = new WebTransportSessionPool();
  const parts = makeParts();

  const a = mustOpen(pool, parts, "https://a.example.com");
  const conn = pool.getOrCreateConnection(parts);
  conn.originFrame = { validated: true, origins: ["https://b.example.com"] };

  const bRes = openSession(pool, parts, "https://b.example.com");
  assert.ok(bRes.ok && bRes.pooled, "authorized origin shares the connection");
  const b = bRes.ok ? bRes.handle : null;
  assert.ok(b !== null);
  assert.equal(pool.connectionCount, 1);

  // Shared by design: session IDs, stream-ID map, congestion stats.
  assert.notEqual(a.session.id, b.session.id);
  const streamA = conn.allocateStreamId(a.session.id);
  const streamB = conn.allocateStreamId(b.session.id);
  assert.equal(pool.sessionForStream(conn.generation, streamA)?.id, a.session.id);
  assert.equal(pool.sessionForStream(conn.generation, streamB)?.id, b.session.id);
  conn.congestion.bytesInFlight = 1500;
  assert.equal(pool.congestionStats(conn.generation)?.bytesInFlight, 1500);

  // Never shared: per-origin application state.
  pool.routeDatagram(conn.generation, a.session.id, new Uint8Array([0xaa]));
  assert.equal(a.session.datagramQueue.length, 1);
  assert.equal(b.session.datagramQueue.length, 0);
  assert.equal(b.session.appStats.datagramsReceived, 0);
  assert.notEqual(a.session.url, undefined);
  assert.equal(a.session.origin, "https://a.example.com");
  assert.equal(b.session.origin, "https://b.example.com");
  assert.equal(a.session.draft, "draft-ietf-webtrans-http3-02");
});

// Connection identity: partition key / proxy / ECH / ALPN / endpoint all matter.
test("connection key binds full identity, not just IP+cert", () => {
  const base = makeParts();
  const baseKey = canonicalizeKey(base);
  const variants: ConnectionKeyParts[] = [
    makeParts({ partitionKey: { topLevelSite: "https://other.com" } }),
    makeParts({ proxyChain: ["https://proxy.example:8443"] }),
    makeParts({ ech: { kind: "accepted", publicName: "public.example" } }),
    makeParts({ alpn: "h3-29" }),
    makeParts({ remote: { ip: "192.0.2.2", port: 443 } }),
    makeParts({
      cert: { ...base.cert, chainFingerprint: "sha256:bbb" },
    }),
  ];
  for (const v of variants) {
    assert.notEqual(canonicalizeKey(v), baseKey);
  }
  assert.equal(canonicalizeKey(makeParts()), baseKey);
});
