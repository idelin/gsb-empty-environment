import assert from "node:assert/strict";
import test from "node:test";

import {
  LoginSessionService,
  PermissionGenerationConflictError,
  digestDevice,
} from "./index.ts";

const ATTACKER_DEVICE = digestDevice("attacker", "browser-a");
const VICTIM_DEVICE = digestDevice("victim", "browser-b");

function sequentialIds(prefix = "server-session") {
  let nextId = 0;
  return () => `${prefix}-${++nextId}`;
}

test("login rotates a presented session ID and never authenticates the fixed ID", () => {
  const service = new LoginSessionService({ idGenerator: sequentialIds() });
  const attacker = service.login({
    userId: "attacker",
    deviceDigest: ATTACKER_DEVICE,
  });

  const victim = service.login({
    userId: "victim",
    deviceDigest: VICTIM_DEVICE,
    presentedSessionId: attacker.sessionId,
  });

  assert.notEqual(victim.sessionId, attacker.sessionId);
  assert.equal(victim.rotatedFrom, attacker.sessionId);
  assert.equal(
    service.authenticate({
      sessionId: attacker.sessionId,
      deviceDigest: ATTACKER_DEVICE,
    }),
    null,
  );
  assert.equal(service.authenticate(attacker.sessionId), null);

  const authenticated = service.authenticate({
    sessionId: victim.sessionId,
    deviceDigest: VICTIM_DEVICE,
  });
  assert.equal(authenticated?.userId, "victim");
  assert.equal(authenticated?.sessionId, victim.sessionId);
});

test("missing, malformed, and never-issued IDs are rejected", () => {
  const service = new LoginSessionService({ idGenerator: sequentialIds() });

  assert.equal(service.inspect(undefined).reason, "missing_session_id");
  assert.equal(service.inspect({}).reason, "missing_session_id");
  assert.equal(
    service.inspect("client fixed id with spaces").reason,
    "malformed_session_id",
  );
  assert.equal(
    service.inspect("attacker-chosen-session").reason,
    "unknown_session_id",
  );

  const session = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
    presentedSessionId: "attacker-chosen-session",
  });
  assert.notEqual(session.sessionId, "attacker-chosen-session");
  assert.equal(service.authenticate("attacker-chosen-session"), null);
  assert.equal(
    service.authenticate({
      sessionId: session.sessionId,
      deviceDigest: VICTIM_DEVICE,
    })?.userId,
    "alice",
  );
});

test("concurrent login policy is explicit and IDs remain unique", () => {
  const singleService = new LoginSessionService({
    idGenerator: sequentialIds("single"),
  });
  const first = singleService.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });
  const second = singleService.login({
    userId: "alice",
    deviceDigest: ATTACKER_DEVICE,
  });

  assert.notEqual(first.sessionId, second.sessionId);
  assert.equal(singleService.authenticate(first.sessionId), null);
  assert.equal(
    singleService.authenticate(second.sessionId)?.deviceDigest,
    ATTACKER_DEVICE,
  );

  const allowService = new LoginSessionService({
    idGenerator: sequentialIds("allow"),
    concurrentLoginPolicy: "allow",
  });
  const allowFirst = allowService.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });
  const allowSecond = allowService.login({
    userId: "alice",
    deviceDigest: ATTACKER_DEVICE,
  });

  assert.notEqual(allowFirst.sessionId, allowSecond.sessionId);
  assert.equal(
    allowService.authenticate(allowFirst.sessionId)?.userId,
    "alice",
  );
  assert.equal(
    allowService.authenticate(allowSecond.sessionId)?.userId,
    "alice",
  );
});

test("limited concurrent sessions evict the oldest session", () => {
  let now = 1_000;
  const service = new LoginSessionService({
    clock: () => now,
    idGenerator: sequentialIds("limited"),
    concurrentLoginPolicy: "limit",
    maxSessionsPerUser: 2,
  });

  const first = service.login({ userId: "alice", deviceDigest: "device-1" });
  now += 1;
  const second = service.login({ userId: "alice", deviceDigest: "device-2" });
  now += 1;
  const third = service.login({ userId: "alice", deviceDigest: "device-3" });

  assert.equal(service.authenticate(first.sessionId), null);
  assert.equal(service.authenticate(second.sessionId)?.userId, "alice");
  assert.equal(service.authenticate(third.sessionId)?.userId, "alice");
});

test("permission changes revoke all old sessions and advance the generation", () => {
  const service = new LoginSessionService({ idGenerator: sequentialIds() });
  const before = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
    permissionGeneration: 0,
  });

  const revoked = service.changePermissions("alice");
  assert.equal(revoked, 1);
  assert.equal(service.getPermissionGeneration("alice"), 1);
  assert.equal(service.inspect(before.sessionId).reason, "unknown_session_id");
  assert.equal(service.authenticate(before.sessionId), null);

  // Even if a stale record survived in an external store, lookup rechecks the
  // server-side permission generation instead of trusting the record alone.
  service.store.set(before.session);
  assert.equal(
    service.inspect(before.sessionId).reason,
    "permission_generation_mismatch",
  );

  const after = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });
  assert.equal(after.session.permissionGeneration, 1);
  assert.equal(service.authenticate(after.sessionId)?.userId, "alice");

  const authorityLogin = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
    permissionGeneration: 2,
  });
  assert.equal(service.authenticate(after.sessionId), null);
  assert.equal(authorityLogin.session.permissionGeneration, 2);

  assert.throws(
    () =>
      service.login({
        userId: "alice",
        deviceDigest: VICTIM_DEVICE,
        permissionGeneration: 1,
      }),
    PermissionGenerationConflictError,
  );
});

test("client-declared identity and permission claims cannot elevate a session", () => {
  const service = new LoginSessionService({ idGenerator: sequentialIds() });
  const session = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
    permissionGeneration: 3,
  });

  const authenticated = service.authenticate({
    sessionId: session.sessionId,
    deviceDigest: VICTIM_DEVICE,
    userId: "admin",
    permissionGeneration: 999,
    deviceFingerprint: "trusted-looking-but-ignored",
  });

  assert.equal(authenticated?.userId, "alice");
  assert.equal(authenticated?.permissionGeneration, 3);
  assert.equal(
    service.inspect({
      sessionId: session.sessionId,
      userId: "admin",
    }).session?.userId,
    "alice",
  );
});

test("password reset immediately invalidates every existing session", () => {
  const service = new LoginSessionService({
    idGenerator: sequentialIds(),
    concurrentLoginPolicy: "allow",
  });
  const first = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });
  const second = service.login({
    userId: "alice",
    deviceDigest: ATTACKER_DEVICE,
  });

  assert.equal(service.resetPassword("alice"), 2);
  assert.equal(service.authenticate(first.sessionId), null);
  assert.equal(service.authenticate(second.sessionId), null);

  service.store.set(first.session);
  assert.equal(
    service.inspect(first.sessionId).reason,
    "credential_revoked",
  );

  const afterReset = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });
  assert.equal(service.authenticate(afterReset.sessionId)?.userId, "alice");
  assert.ok(afterReset.session.credentialGeneration > 0);
});

test("logout invalidates the ID immediately and cannot be replayed", () => {
  const service = new LoginSessionService({ idGenerator: sequentialIds() });
  const session = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });

  assert.equal(
    service.logout({
      sessionId: session.sessionId,
      deviceDigest: ATTACKER_DEVICE,
    }),
    false,
  );
  assert.equal(
    service.authenticate({
      sessionId: session.sessionId,
      deviceDigest: VICTIM_DEVICE,
    })?.userId,
    "alice",
  );

  assert.equal(
    service.logout({
      sessionId: session.sessionId,
      deviceDigest: VICTIM_DEVICE,
    }),
    true,
  );
  assert.equal(service.authenticate(session.sessionId), null);
  assert.equal(service.logout(session.sessionId), false);

  const fresh = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
    presentedSessionId: session.sessionId,
  });
  assert.notEqual(fresh.sessionId, session.sessionId);
  assert.equal(service.authenticate(fresh.sessionId)?.userId, "alice");
});

test("device digest is bound to the session", () => {
  const service = new LoginSessionService({ idGenerator: sequentialIds() });
  const session = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });

  assert.equal(
    service.inspect({
      sessionId: session.sessionId,
      deviceDigest: ATTACKER_DEVICE,
    }).reason,
    "device_mismatch",
  );
  assert.equal(
    service.authenticate({
      sessionId: session.sessionId,
      deviceDigest: VICTIM_DEVICE,
    })?.deviceDigest,
    VICTIM_DEVICE,
  );
});

test("legitimate renewal extends idle expiry without changing authentication time", () => {
  let now = 1_000;
  const service = new LoginSessionService({
    clock: () => now,
    idGenerator: sequentialIds(),
    idleTimeoutMs: 10 * 60 * 1000,
    absoluteTimeoutMs: 30 * 60 * 1000,
  });
  const session = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });

  now += 9 * 60 * 1000;
  const renewed = service.renew({
    sessionId: session.sessionId,
    deviceDigest: VICTIM_DEVICE,
  });
  assert.equal(renewed?.sessionId, session.sessionId);
  assert.equal(renewed?.authenticatedAt, session.session.authenticatedAt);
  assert.equal(renewed?.expiresAt, now + 10 * 60 * 1000);

  now += 9 * 60 * 1000;
  assert.equal(
    service.authenticate({
      sessionId: session.sessionId,
      deviceDigest: VICTIM_DEVICE,
    })?.userId,
    "alice",
  );

  now += 13 * 60 * 1000;
  assert.equal(service.authenticate(session.sessionId), null);
});

test("rotated renewal makes the authentication state belong only to the new ID", () => {
  let now = 10_000;
  const service = new LoginSessionService({
    clock: () => now,
    idGenerator: sequentialIds("renewed"),
    rotateOnRenew: true,
  });
  const original = service.login({
    userId: "alice",
    deviceDigest: VICTIM_DEVICE,
  });

  now += 1_000;
  const renewed = service.renew(original.sessionId);
  assert.ok(renewed);
  assert.notEqual(renewed.sessionId, original.sessionId);
  assert.equal(renewed.authenticatedAt, original.session.authenticatedAt);
  assert.equal(service.authenticate(original.sessionId), null);
  assert.equal(service.authenticate(renewed.sessionId)?.userId, "alice");
});
