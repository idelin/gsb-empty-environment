import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import test from "node:test";

import {
  DEVICE_BINDING_COOKIE_NAME,
  ISSUANCE_WINDOW_MS,
  LOGIN_LINK_TTL_MS,
  createLoginLinkService,
  getDeviceBindingFromCookie,
  type LoginLinkIssuance,
} from "../index.ts";

function binding(): string {
  return randomBytes(16).toString("base64url");
}

function hash(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

async function issue(
  service: ReturnType<typeof createLoginLinkService>,
  email = "user@example.com",
): Promise<LoginLinkIssuance> {
  const result = await service.issueLoginLink(email);
  assert.equal(result.ok, true);
  return result as LoginLinkIssuance;
}

test("issues 128-bit binding and 256-bit token while storing only hashes", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);

  assert.equal(issued.token.length, 43);
  assert.equal(issued.deviceBinding.length, 22);
  assert.match(
    issued.cookie,
    new RegExp(`^${DEVICE_BINDING_COOKIE_NAME}=${issued.deviceBinding};`),
  );
  assert.match(issued.cookie, /; HttpOnly;/);
  assert.match(issued.cookie, /; Secure;/);
  assert.match(issued.cookie, /; SameSite=Lax;/);

  const links = (
    service as unknown as {
      linksByTokenHash: Map<
        string,
        { tokenHash: string; deviceHash: string }
      >;
    }
  ).linksByTokenHash;
  const stored = links.get(hash(issued.token));
  assert.ok(stored);
  assert.equal(stored.deviceHash, hash(issued.deviceBinding));
  assert.equal(stored.tokenHash, hash(issued.token));
  assert.notEqual(stored.tokenHash, issued.token);
  assert.notEqual(stored.deviceHash, issued.deviceBinding);
});

test("scanner GET prefetches do not consume the token", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);

  for (let count = 0; count < 3; count += 1) {
    const response = service.handleGet(issued.token);
    assert.equal(response.status, 200);
    assert.equal(response.headers["Set-Cookie"], undefined);
    assert.match(response.body, /<form method="post"/);
    assert.match(response.body, /确认登录/);
  }

  const result = await service.redeem(issued.token, issued.deviceBinding);
  assert.equal(result.ok, true);
  assert.equal(result.consumed, true);
});

test("a token opened in another browser does not log in that browser", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service, "attacker@example.com");

  const victimAttempt = await service.redeem(issued.token, binding());
  assert.equal(victimAttempt.ok, false);
  assert.equal(victimAttempt.code, "device_binding_required");
  assert.equal(victimAttempt.fallback, "email_code");
  assert.equal(victimAttempt.consumed, false);

  const attackerAttempt = await service.redeem(
    issued.token,
    issued.deviceBinding,
  );
  assert.equal(attackerAttempt.ok, true);
});

test("missing device binding falls back to email code without consuming", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);

  const missing = await service.redeem({ token: issued.token });
  assert.equal(missing.ok, false);
  assert.equal(missing.code, "device_binding_required");
  assert.equal(missing.fallback, "email_code");
  assert.match(missing.message, /验证码/);

  const retry = await service.redeem(issued.token, issued.deviceBinding);
  assert.equal(retry.ok, true);
});

test("tokens are one-time and replay does not create another session", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);

  const first = await service.redeem(issued.token, issued.deviceBinding);
  assert.equal(first.ok, true);
  const replay = await service.redeem(issued.token, issued.deviceBinding);
  assert.equal(replay.ok, false);
  assert.equal(replay.code, "invalid_or_expired_token");
  assert.equal(replay.consumed, false);
  assert.equal(service.hasActiveSession(first.sessionId), true);
});

test("only five tokens can be issued per email in ten minutes", async () => {
  let now = 1_000;
  const service = createLoginLinkService({ now: () => now });
  const issued: LoginLinkIssuance[] = [];
  for (let count = 0; count < 5; count += 1) {
    issued.push(await issue(service));
  }

  const first = issued[0] as LoginLinkIssuance;
  const fifth = issued[4] as LoginLinkIssuance;
  const sixth = await service.issueLoginLink(" USER@example.com ");
  assert.equal(sixth.ok, false);
  assert.equal(sixth.code, "rate_limited");
  assert.equal(sixth.status, 429);

  const invalidated = await service.redeem(first.token, first.deviceBinding);
  assert.equal(invalidated.ok, false);
  const current = await service.redeem(fifth.token, fifth.deviceBinding);
  assert.equal(current.ok, true);

  now += ISSUANCE_WINDOW_MS;
  const afterWindow = await service.issueLoginLink("user@example.com");
  assert.equal(afterWindow.ok, true);
});

test("concurrent issuance cannot bypass the five-token limit", async () => {
  const service = createLoginLinkService();
  const results = await Promise.all(
    Array.from({ length: 8 }, () => service.issueLoginLink("race@example.com")),
  );

  assert.equal(results.filter((result) => result.ok).length, 5);
  assert.equal(
    results.filter(
      (result) => !result.ok && result.code === "rate_limited",
    ).length,
    3,
  );
});

test("new issuance invalidates the previous link for the same email", async () => {
  const service = createLoginLinkService();
  const first = await issue(service);
  const second = await issue(service);

  const old = await service.redeem(first.token, first.deviceBinding);
  assert.equal(old.ok, false);
  assert.equal(old.code, "invalid_or_expired_token");

  const current = await service.redeem(second.token, second.deviceBinding);
  assert.equal(current.ok, true);
});

test("links expire after fifteen minutes", async () => {
  let now = 100;
  const service = createLoginLinkService({ now: () => now });
  const issued = await issue(service);

  now += LOGIN_LINK_TTL_MS - 1;
  const justInTime = await service.redeem(
    issued.token,
    issued.deviceBinding,
  );
  assert.equal(justInTime.ok, true);

  now += 1;
  const expiredIssued = await issue(service);
  now += LOGIN_LINK_TTL_MS;
  const expired = await service.redeem(
    expiredIssued.token,
    expiredIssued.deviceBinding,
  );
  assert.equal(expired.ok, false);
  assert.equal(expired.code, "invalid_or_expired_token");
});

test("successful redemption rotates the session identifier", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);
  const previousSessionId = "attacker-known-session";

  const result = await service.redeem(
    issued.token,
    issued.deviceBinding,
    previousSessionId,
  );
  assert.equal(result.ok, true);
  assert.notEqual(result.sessionId, previousSessionId);
  assert.equal(result.previousSessionId, previousSessionId);
  assert.equal(result.rotated, true);
  assert.equal(service.hasActiveSession(previousSessionId), false);
  assert.equal(service.hasActiveSession(result.sessionId), true);
  assert.equal(service.getSessionEmail(result.sessionId), "user@example.com");
});

test("POST reads the HTTP-only binding cookie and sets a new session cookie", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);
  const cookieHeaderValue = `theme=dark; ${DEVICE_BINDING_COOKIE_NAME}=${issued.deviceBinding}`;

  assert.equal(
    getDeviceBindingFromCookie(cookieHeaderValue),
    issued.deviceBinding,
  );
  const response = await service.handlePost({
    token: issued.token,
    cookieHeader: cookieHeaderValue,
  });
  assert.equal(response.status, 200);
  assert.match(response.headers["Set-Cookie"] ?? "", /^__Host-session=/);
  const body = JSON.parse(response.body) as { ok: boolean; consumed: boolean };
  assert.equal(body.ok, true);
  assert.equal(body.consumed, true);

  const fallback = await service.handlePost({ token: issued.token });
  assert.equal(fallback.status, 400);
  assert.match(fallback.body, /email_code/);
});

test("concurrent redemption succeeds at most once", async () => {
  const service = createLoginLinkService();
  const issued = await issue(service);

  const results = await Promise.all([
    service.redeem(issued.token, issued.deviceBinding),
    service.redeem(issued.token, issued.deviceBinding),
  ]);
  assert.equal(results.filter((result) => result.ok).length, 1);
});
