import { test } from "node:test";
import assert from "node:assert/strict";

import {
  CertificateMismatchError,
  ConnectionNotCurrentError,
  DuplicateSessionIdError,
  OriginFrameRejectedError,
  OriginNotAuthorizedError,
  QuotaExceededError,
  SessionAllocationFrozenError,
  WebTransportSessionPool,
  certificateCovers,
  type ConnectionDescriptor,
  type SessionEstablishment,
  type VerifiedServerCertificate,
} from "../index.ts";

const SHARED_CERTIFICATE: VerifiedServerCertificate = {
  fingerprintSha256: "sha256:shared-cert-v1",
  dnsNames: ["a.test", "b.test"],
  ipAddresses: ["203.0.113.10"],
};

function descriptor(
  overrides: Partial<ConnectionDescriptor> = {},
): ConnectionDescriptor {
  return {
    networkPartitionKey: "partition-1",
    proxyChain: ["direct"],
    certificate: SHARED_CERTIFICATE,
    ech: { status: "not_offered" },
    alpn: "h3",
    remoteEndpoint: { address: "203.0.113.10", port: 443 },
    quicGeneration: 1,
    initialOrigin: "https://a.test",
    ...overrides,
  };
}

function establishment(
  sessionId: string,
  overrides: Partial<SessionEstablishment> = {},
): SessionEstablishment {
  return {
    sessionId,
    draft: "draft-ietf-webtransport-http3-13",
    version: "13",
    streamLimit: 8,
    datagramLimit: 16,
    ...overrides,
  };
}

function deferred<T>(): {
  readonly promise: Promise<T>;
  readonly resolve: (value: T) => void;
  readonly reject: (reason?: unknown) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test("same certificate is not same-origin authorization", async () => {
  const pool = new WebTransportSessionPool();
  const connection = pool.openConnection(descriptor());

  const sessionA = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/alpha",
    establishment: establishment("session-a"),
  });
  assert.equal(sessionA.origin, "https://a.test");

  await assert.rejects(
    pool.createSession({
      connectionId: connection.id,
      url: "https://b.test/beta",
      establishment: establishment("session-b"),
    }),
    (error: unknown) => error instanceof OriginNotAuthorizedError,
  );

  await assert.rejects(
    pool.createSession({
      connectionId: connection.id,
      url: "https://evil.test/steal",
      establishment: establishment("session-evil"),
    }),
    (error: unknown) => error instanceof CertificateMismatchError,
  );

  const frame = pool.receiveOriginFrame(connection.id, {
    generation: connection.generation,
    origins: ["https://b.test"],
    source: "http3-control-stream",
  });
  assert.deepEqual(frame.authorizedOrigins, ["https://a.test", "https://b.test"]);

  const sessionB = await pool.createSession({
    connectionId: connection.id,
    url: "https://b.test/beta",
    establishment: establishment("session-b"),
  });
  assert.equal(sessionB.origin, "https://b.test");
  assert.equal(pool.getSessionSummary(sessionB).canonicalUrl, "https://b.test/beta");
  assert.equal(pool.getConnectionSummary(connection.id)?.activeSessions, 2);

  // The coalesced session must not inherit A's URL, quotas, streams, or queue.
  assert.equal(pool.getSessionSummary(sessionA).canonicalUrl, "https://a.test/alpha");
  assert.deepEqual(pool.pollDatagrams(sessionB), []);
});

test("forged ORIGIN frames are rejected atomically", async () => {
  const pool = new WebTransportSessionPool();
  const aOnlyCertificate: VerifiedServerCertificate = {
    fingerprintSha256: "sha256:attacker-cert",
    dnsNames: ["a.test"],
    ipAddresses: [],
  };
  const connection = pool.openConnection(
    descriptor({ certificate: aOnlyCertificate }),
  );

  // A certificate covering only a.test cannot claim b.test.
  assert.throws(
    () =>
      pool.receiveOriginFrame(connection.id, {
        generation: connection.generation,
        origins: ["https://b.test"],
        source: "http3-control-stream",
      }),
    (error: unknown) => error instanceof OriginFrameRejectedError,
  );

  // Frames are atomic: one covered origin cannot smuggle in one uncovered origin.
  assert.throws(
    () =>
      pool.receiveOriginFrame(connection.id, {
        generation: connection.generation,
        origins: ["https://a.test", "https://b.test"],
        source: "http3-control-stream",
      }),
    (error: unknown) => error instanceof OriginFrameRejectedError,
  );
  assert.deepEqual(
    pool.getConnectionSummary(connection.id)?.authorizedOrigins,
    ["https://a.test"],
  );

  // Application-layer input is not the authenticated control stream.
  assert.throws(
    () =>
      pool.receiveOriginFrame(connection.id, {
        generation: connection.generation,
        origins: ["https://a.test"],
        source: "application",
      } as never),
    (error: unknown) =>
      error instanceof OriginFrameRejectedError &&
      error.code === "origin_frame_trust_failure",
  );

  // Authorize b.test on generation 1 only. Opening generation 2 must not
  // inherit that frame or permit b.test without a fresh authenticated frame.
  const sharedPool = new WebTransportSessionPool();
  const generation1 = sharedPool.openConnection(descriptor({ quicGeneration: 1 }));
  sharedPool.receiveOriginFrame(generation1.id, {
    generation: 1,
    origins: ["https://b.test"],
    source: "http3-control-stream",
  });
  const generation2 = sharedPool.openConnection(descriptor({ quicGeneration: 2 }));
  assert.throws(
    () =>
      sharedPool.receiveOriginFrame(generation2.id, {
        generation: 1,
        origins: ["https://b.test"],
        source: "http3-control-stream",
      }),
    (error: unknown) => error instanceof OriginFrameRejectedError,
  );
  await assert.rejects(
    sharedPool.createSession({
      connectionId: generation2.id,
      url: "https://b.test/beta",
      establishment: establishment("replayed-frame"),
    }),
    (error: unknown) => error instanceof OriginNotAuthorizedError,
  );
});

test("session IDs are per-connection and late datagrams use generation + session ID", async () => {
  const pool = new WebTransportSessionPool();
  const generation1 = pool.openConnection(descriptor({ quicGeneration: 1 }));
  const first = await pool.createSession({
    connectionId: generation1.id,
    url: "https://a.test/one",
    establishment: establishment("reused-id"),
  });

  await assert.rejects(
    pool.createSession({
      connectionId: generation1.id,
      url: "https://a.test/two",
      establishment: establishment("reused-id"),
    }),
    (error: unknown) => error instanceof DuplicateSessionIdError,
  );

  const second = await pool.createSession({
    connectionId: generation1.id,
    url: "https://a.test/two",
    establishment: establishment("other-id"),
  });
  assert.equal(pool.closeSession(second), true);
  await assert.rejects(
    pool.createSession({
      connectionId: generation1.id,
      url: "https://a.test/three",
      establishment: establishment("other-id"),
    }),
    (error: unknown) => error instanceof DuplicateSessionIdError,
  );

  // A new generation may reuse the numeric server session ID, but a delayed
  // datagram for the old generation must never enter the new session.
  const generation2 = pool.openConnection(descriptor({ quicGeneration: 2 }));
  const replacement = await pool.createSession({
    connectionId: generation2.id,
    url: "https://a.test/replacement",
    establishment: establishment("reused-id"),
  });
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 1,
      sessionId: "reused-id",
      payload: new Uint8Array([1, 2, 3]),
    }),
    false,
  );
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 2,
      sessionId: "reused-id",
      payload: new Uint8Array([4, 5, 6]),
    }),
    true,
  );
  assert.deepEqual(pool.pollDatagrams(replacement), [new Uint8Array([4, 5, 6])]);

  // Unknown and closed IDs are silently dropped.
  assert.equal(
    pool.deliverDatagram(2, "unknown-id", new Uint8Array([7])),
    false,
  );
  assert.equal(pool.closeSession(first), true);
  assert.equal(
    pool.deliverDatagram(1, first.sessionId, new Uint8Array([8])),
    false,
  );
});

test("network switch freezes old allocation and retires its generation", async () => {
  const pool = new WebTransportSessionPool();
  const generation1 = pool.openConnection(descriptor({ quicGeneration: 1 }));
  const oldSession = await pool.createSession({
    connectionId: generation1.id,
    url: "https://a.test/old",
    establishment: establishment("shared"),
  });

  assert.equal(
    pool.handleNetworkSwitch(generation1.id, { generation: 1 }),
    true,
  );
  await assert.rejects(
    pool.createSession({
      connectionId: generation1.id,
      url: "https://a.test/after-switch",
      establishment: establishment("after-switch"),
    }),
    (error: unknown) =>
      error instanceof SessionAllocationFrozenError &&
      error.reasons.includes("network_switch"),
  );
  assert.throws(
    () => pool.sendDatagram(oldSession, new Uint8Array([1])),
    (error: unknown) => error instanceof ConnectionNotCurrentError,
  );
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 1,
      sessionId: "shared",
      payload: new Uint8Array([9]),
    }),
    false,
  );

  const generation2 = pool.openConnection(descriptor({ quicGeneration: 2 }));
  const newSession = await pool.createSession({
    connectionId: generation2.id,
    url: "https://a.test/new",
    establishment: establishment("shared"),
  });
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 2,
      sessionId: "shared",
      payload: new Uint8Array([10]),
    }),
    true,
  );
  assert.deepEqual(pool.pollDatagrams(newSession), [new Uint8Array([10])]);
  assert.equal(pool.pollDatagrams(oldSession).length, 0);
});

test("GOAWAY racing a CONNECT never commits the new session", async () => {
  const pending = deferred<SessionEstablishment>();
  const pool = new WebTransportSessionPool({
    establishSession: () => pending.promise,
  });
  const connection = pool.openConnection(descriptor({ quicGeneration: 1 }));

  const create = pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/race",
  });
  await Promise.resolve();

  assert.equal(
    pool.handleGoAway(connection.id, { generation: 1 }),
    true,
  );
  pending.resolve(establishment("raced-session"));
  await assert.rejects(
    create,
    (error: unknown) =>
      error instanceof SessionAllocationFrozenError &&
      error.reasons.includes("goaway"),
  );
  assert.equal(pool.getConnectionSummary(connection.id)?.activeSessions, 0);

  // A stale GOAWAY for generation 1 cannot freeze generation 2.
  const generation2 = pool.openConnection(descriptor({ quicGeneration: 2 }));
  assert.equal(
    pool.handleGoAway(generation2.id, { generation: 1 }),
    false,
  );
  const fresh = await pool.createSession({
    connectionId: generation2.id,
    url: "https://a.test/fresh",
    establishment: establishment("fresh-session"),
  });
  assert.equal(pool.getSessionSummary(fresh).state, "open");
});

test("GOAWAY stops new sessions but lets existing sessions drain", async () => {
  const pool = new WebTransportSessionPool();
  const connection = pool.openConnection(descriptor({ quicGeneration: 1 }));
  const existing = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/existing",
    establishment: establishment("existing-session"),
  });
  pool.handleGoAway(connection.id, { generation: 1 });

  await assert.rejects(
    pool.createSession({
      connectionId: connection.id,
      url: "https://a.test/new",
      establishment: establishment("new-session"),
    }),
    (error: unknown) => error instanceof SessionAllocationFrozenError,
  );
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 1,
      sessionId: "existing-session",
      payload: new Uint8Array([1]),
    }),
    true,
  );
  assert.deepEqual(pool.pollDatagrams(existing), [new Uint8Array([1])]);
});

test("certificate update freezes the old verified identity", async () => {
  const pool = new WebTransportSessionPool();
  const connection = pool.openConnection(
    descriptor({
      quicGeneration: 1,
      certificate: {
        fingerprintSha256: "sha256:old-cert",
        dnsNames: ["a.test", "b.test"],
        ipAddresses: [],
      },
    }),
  );
  const existing = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/existing",
    establishment: establishment("existing-session"),
  });
  const updatedCertificate: VerifiedServerCertificate = {
    fingerprintSha256: "sha256:new-cert",
    dnsNames: ["a.test", "b.test"],
    ipAddresses: [],
  };
  assert.equal(
    pool.handleCertificateUpdate(connection.id, {
      generation: 1,
      newCertificate: updatedCertificate,
    }),
    true,
  );
  await assert.rejects(
    pool.createSession({
      connectionId: connection.id,
      url: "https://a.test/new",
      establishment: establishment("new-session"),
    }),
    (error: unknown) =>
      error instanceof SessionAllocationFrozenError &&
      error.reasons.includes("certificate_update"),
  );
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 1,
      sessionId: "existing-session",
      payload: new Uint8Array([2]),
    }),
    true,
  );
  assert.deepEqual(pool.pollDatagrams(existing), [new Uint8Array([2])]);

  const newConnection = pool.openConnection(
    descriptor({
      quicGeneration: 2,
      certificate: updatedCertificate,
      initialOrigin: "https://b.test",
    }),
  );
  const newSession = await pool.createSession({
    connectionId: newConnection.id,
    url: "https://b.test/new",
    establishment: establishment("new-cert-session"),
  });
  assert.equal(newSession.origin, "https://b.test");
});

test("0-RTT rejection closes early sessions and freezes new allocation", async () => {
  const pool = new WebTransportSessionPool();
  const connection = pool.openConnection(descriptor({ quicGeneration: 1 }));
  const early = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/early",
    earlyData: true,
    establishment: establishment("early-session"),
  });
  const oneRtt = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/full",
    establishment: establishment("one-rtt-session"),
  });

  assert.equal(pool.rejectZeroRtt(connection.id, { generation: 1 }), 1);
  assert.equal(pool.getSessionSummary(early).state, "closed");
  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 1,
      sessionId: "early-session",
      payload: new Uint8Array([1]),
    }),
    false,
  );

  await assert.rejects(
    pool.createSession({
      connectionId: connection.id,
      url: "https://a.test/after-reject",
      establishment: establishment("after-reject"),
    }),
    (error: unknown) =>
      error instanceof SessionAllocationFrozenError &&
      error.reasons.includes("zero_rtt_rejection"),
  );

  assert.equal(
    pool.deliverDatagram({
      quicGeneration: 1,
      sessionId: "one-rtt-session",
      payload: new Uint8Array([2]),
    }),
    true,
  );
  assert.deepEqual(pool.pollDatagrams(oneRtt), [new Uint8Array([2])]);
});

test("quotas, stream mapping, and application state stay per-origin", async () => {
  const pool = new WebTransportSessionPool();
  const connection = pool.openConnection(
    descriptor({
      quicGeneration: 1,
      certificate: SHARED_CERTIFICATE,
    }),
  );
  pool.receiveOriginFrame(connection.id, {
    generation: 1,
    origins: ["https://b.test"],
    source: "http3-control-stream",
  });

  const sessionA = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/a",
    establishment: establishment("a-session", {
      streamLimit: 2,
      datagramLimit: 1,
    }),
  });
  const sessionB = await pool.createSession({
    connectionId: connection.id,
    url: "https://b.test/b",
    establishment: establishment("b-session", {
      streamLimit: 1,
      datagramLimit: 1,
    }),
  });

  const aStream1 = pool.openStream(sessionA);
  const aStream2 = pool.openStream(sessionA);
  assert.equal(aStream1.streamId, 1);
  assert.equal(aStream2.streamId, 2);
  assert.throws(
    () => pool.openStream(sessionA),
    (error: unknown) =>
      error instanceof QuotaExceededError && error.resource === "stream",
  );

  const bStream = pool.openStream(sessionB);
  assert.equal(bStream.streamId, 3);
  assert.throws(
    () => pool.openStream(sessionB),
    (error: unknown) =>
      error instanceof QuotaExceededError && error.resource === "stream",
  );

  const aDatagram = pool.sendDatagram(sessionA, new Uint8Array([65]));
  assert.equal(aDatagram.sessionId, "a-session");
  assert.throws(
    () => pool.sendDatagram(sessionA, new Uint8Array([66])),
    (error: unknown) =>
      error instanceof QuotaExceededError && error.resource === "datagram",
  );
  const bDatagram = pool.sendDatagram(sessionB, new Uint8Array([66]));
  assert.equal(bDatagram.sessionId, "b-session");

  assert.equal(pool.deliverDatagram(bDatagram), true);
  assert.deepEqual(pool.pollDatagrams(sessionB), [new Uint8Array([66])]);
  assert.deepEqual(pool.pollDatagrams(sessionA), []);

  pool.recordCongestion(sessionA, { smoothedRttMs: 28, packetsInFlight: 4 });
  const seenByB = pool.getCongestionStats(sessionB);
  assert.equal(seenByB.samples, 1);
  assert.equal(seenByB.last.smoothedRttMs, 28);
  assert.equal(pool.getSessionSummary(sessionA).origin, "https://a.test");
  assert.equal(pool.getSessionSummary(sessionB).origin, "https://b.test");
});

test("ECH, ALPN, proxy chain, partition, endpoint, and generation are separate identities", async () => {
  const pool = new WebTransportSessionPool();
  const base = pool.openConnection(descriptor({ quicGeneration: 1 }));
  const changedEch = pool.openConnection(
    descriptor({
      quicGeneration: 2,
      ech: { status: "accepted", configId: 7 },
    }),
  );
  const changedAlpn = pool.openConnection(
    descriptor({ quicGeneration: 3, alpn: "h3-29" }),
  );
  const changedProxy = pool.openConnection(
    descriptor({ quicGeneration: 4, proxyChain: ["https://proxy.test:443"] }),
  );
  const changedPartition = pool.openConnection(
    descriptor({ quicGeneration: 5, networkPartitionKey: "partition-2" }),
  );
  const changedEndpoint = pool.openConnection(
    descriptor({
      quicGeneration: 6,
      remoteEndpoint: { address: "203.0.113.11", port: 443 },
    }),
  );

  const frames = new Set<string>();
  pool.receiveOriginFrame(base.id, {
    generation: 1,
    origins: ["https://b.test"],
    source: "http3-control-stream",
  });
  for (const connection of [
    changedEch,
    changedAlpn,
    changedProxy,
    changedPartition,
    changedEndpoint,
  ]) {
    await assert.rejects(
      pool.createSession({
        connectionId: connection.id,
        url: "https://b.test/x",
        establishment: establishment(`b-${connection.generation}`),
      }),
      (error: unknown) => error instanceof OriginNotAuthorizedError,
    );
    frames.add(connection.identity.transportKey);
  }
  assert.equal(frames.size, 5);
});

test("certificate coverage handles wildcards and IP SANs without overmatching", () => {
  const certificate: VerifiedServerCertificate = {
    fingerprintSha256: "sha256:wildcard",
    dnsNames: ["*.example.test", "exact.test"],
    ipAddresses: ["203.0.113.9", "2001:db8::1"],
  };
  assert.equal(certificateCovers(certificate, "https://a.example.test"), true);
  assert.equal(certificateCovers(certificate, "https://example.test"), false);
  assert.equal(certificateCovers(certificate, "https://a.b.example.test"), false);
  assert.equal(certificateCovers(certificate, "https://exact.test"), true);
  assert.equal(certificateCovers(certificate, "https://203.0.113.9"), true);
  assert.equal(certificateCovers(certificate, "https://[2001:db8::1]"), true);
  assert.equal(certificateCovers(certificate, "https://[2001:db8::2]"), false);
});

test("generation zero is valid and malformed certificate updates fail closed", async () => {
  const pool = new WebTransportSessionPool();
  const connection = pool.openConnection(
    descriptor({
      quicGeneration: 0,
      remoteEndpoint: { address: "2001:db8::1", port: 443 },
    }),
  );
  const session = await pool.createSession({
    connectionId: connection.id,
    url: "https://a.test/zero",
    establishment: establishment("zero-generation"),
  });
  assert.equal(session.quicGeneration, 0);

  assert.throws(() =>
    pool.handleCertificateUpdate(connection.id, {
      generation: 0,
      newCertificate: {
        fingerprintSha256: "",
        dnsNames: [],
        ipAddresses: [],
      },
    }),
  );
  await assert.rejects(
    pool.createSession({
      connectionId: connection.id,
      url: "https://a.test/after-invalid-update",
      establishment: establishment("after-invalid-update"),
    }),
    (error: unknown) =>
      error instanceof SessionAllocationFrozenError &&
      error.reasons.includes("certificate_update"),
  );
});
