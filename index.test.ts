import test from "node:test";
import assert from "node:assert/strict";

import {
  CookieError,
  CookieManager,
  SessionManager,
  DEFAULT_SESSION_COOKIE_NAME,
  domainMatches,
  normalizeHost,
  parseCookieHeader,
  pathMatches,
} from "./index.ts";

const SECRET = "unit-test-secret-with-enough-entropy";
const NOW = 1_700_000_000_000;
const HOUR = 60 * 60 * 1000;

const parentContext = {
  host: "example.com",
  path: "/",
  secure: true,
} as const;

const childContext = {
  host: "evil.example.com",
  path: "/",
  secure: true,
} as const;

function cookieHeader(name: string, wireValue: string): string {
  return `${name}=${wireValue}`;
}

function hasCode(error: unknown, code: string): boolean {
  return error instanceof CookieError && error.code === code;
}

test("__Host- and __Secure- prefixes are enforced during issuance", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });

  const hostCookie = manager.issue(
    {
      name: "__Host-session",
      value: "session-value",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Lax",
      expires: NOW + HOUR,
    },
    parentContext,
  );
  assert.match(hostCookie.header, /; Path=\//);
  assert.match(hostCookie.header, /; Secure/);
  assert.match(hostCookie.header, /; HttpOnly/);
  assert.doesNotMatch(hostCookie.header, /Domain=/);
  assert.equal(hostCookie.issuer, "example.com");

  assert.throws(
    () =>
      manager.issue(
        {
          name: "__Host-session",
          value: "bad",
          domain: ".example.com",
          path: "/",
          secure: true,
        },
        parentContext,
      ),
    (error: unknown) => hasCode(error, "PREFIX_DOMAIN"),
  );

  assert.throws(
    () =>
      manager.issue(
        {
          name: "__Host-session",
          value: "bad",
          path: "/app",
          secure: true,
        },
        parentContext,
      ),
    (error: unknown) => hasCode(error, "PREFIX_PATH"),
  );

  assert.throws(
    () =>
      manager.issue(
        {
          name: "__Host-session",
          value: "bad",
          path: "/",
          secure: false,
        },
        parentContext,
      ),
    (error: unknown) => hasCode(error, "PREFIX_SECURE"),
  );

  assert.throws(
    () =>
      manager.issue(
        {
          name: "__Secure-session",
          value: "bad",
          path: "/",
          secure: false,
        },
        parentContext,
      ),
    (error: unknown) => hasCode(error, "PREFIX_SECURE"),
  );

  const secureCookie = manager.issue(
    {
      name: "__Secure-session",
      value: "ok",
      path: "/",
      secure: true,
      expires: NOW + HOUR,
    },
    parentContext,
  );
  assert.equal(secureCookie.secure, true);
});

test("a Domain cookie is attributable only to its domain or a narrower subdomain", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });

  const fromApex = manager.issue(
    {
      name: "shared",
      value: "apex",
      domain: ".example.com",
      path: "/",
      secure: true,
      expires: NOW + HOUR,
    },
    parentContext,
  );
  assert.equal(fromApex.domain, "example.com");

  const fromChild = manager.issue(
    {
      name: "shared",
      value: "child",
      domain: ".example.com",
      path: "/",
      secure: true,
      expires: NOW + HOUR,
    },
    childContext,
  );
  assert.equal(fromChild.issuer, "evil.example.com");

  assert.throws(
    () =>
      manager.issue(
        {
          name: "shared",
          value: "bad",
          domain: "example.com",
          path: "/",
          secure: true,
        },
        { host: "notexample.com", path: "/", secure: true },
      ),
    (error: unknown) => hasCode(error, "DOMAIN_NOT_OWNED"),
  );

  assert.equal(domainMatches("example.com", "example.com"), true);
  assert.equal(domainMatches("a.b.example.com", "example.com"), true);
  assert.equal(domainMatches("a.b.example.com", ".example.com"), true);
  assert.equal(domainMatches("example.com", "www.example.com"), false);
  assert.equal(domainMatches("notexample.com", "example.com"), false);
  assert.equal(domainMatches("example.com.evil.test", "example.com"), false);
});

test("host case and port are ignored for domain attribution", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  assert.equal(normalizeHost("EXAMPLE.COM:8443"), "example.com");
  assert.equal(domainMatches("WWW.Example.COM:443", "example.com"), true);

  const issued = manager.issue(
    {
      name: "pref",
      value: "blue",
      domain: ".Example.COM",
      path: "/dash",
      secure: true,
      httpOnly: true,
      sameSite: "Strict",
      expires: NOW + HOUR,
    },
    { host: "EXAMPLE.COM:8443", path: "/dash", secure: true },
  );

  assert.equal(issued.issuer, "example.com");
  assert.equal(issued.domain, "example.com");
  assert.equal(issued.path, "/dash");

  const reception = manager.receive(
    cookieHeader("pref", issued.wireValue),
    { host: "example.COM:443", path: "/dash/settings", secure: true },
  );
  const cookie = reception.get("pref");
  assert.ok(cookie);
  assert.equal(cookie.value, "blue");
  assert.equal(cookie.domain, "example.com");
  assert.equal(cookie.issuer, "example.com");
  assert.equal(cookie.httpOnly, true);
  assert.equal(cookie.sameSite, "Strict");
  assert.equal(cookie.expires, NOW + HOUR);
});

test("signed metadata round-trips and attacker-written values are rejected", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  const issued = manager.issue(
    {
      name: "theme",
      value: "dark",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Strict",
      expires: NOW + HOUR,
    },
    parentContext,
  );

  const valid = manager.receive(
    cookieHeader("theme", issued.wireValue),
    parentContext,
  );
  const cookie = valid.get("theme");
  assert.ok(cookie);
  assert.deepEqual(
    {
      name: cookie.name,
      value: cookie.value,
      domain: cookie.domain,
      path: cookie.path,
      secure: cookie.secure,
      httpOnly: cookie.httpOnly,
      sameSite: cookie.sameSite,
      expires: cookie.expires,
      issuer: cookie.issuer,
    },
    {
      name: "theme",
      value: "dark",
      domain: undefined,
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Strict",
      expires: NOW + HOUR,
      issuer: "example.com",
    },
  );

  const forged = manager.receive("theme=attacker-controlled", parentContext);
  assert.equal(forged.get("theme"), undefined);
  assert.equal(forged.rejected[0].reason, "invalid-signature");

  const tampered =
    issued.wireValue.slice(0, -1) +
    (issued.wireValue.endsWith("A") ? "B" : "A");
  const tamperedResult = manager.receive(
    cookieHeader("theme", tampered),
    parentContext,
  );
  assert.equal(tamperedResult.get("theme"), undefined);
  assert.equal(tamperedResult.rejected[0].reason, "invalid-signature");

  const renamed = manager.receive(
    cookieHeader("other", issued.wireValue),
    parentContext,
  );
  assert.equal(renamed.get("other"), undefined);
  assert.equal(renamed.rejected[0].reason, "name-mismatch");
});

test("a child host cannot replace the parent host __Host- session", () => {
  const sessions = new SessionManager({
    secret: SECRET,
    now: () => NOW,
  });
  const login = sessions.login(parentContext, "user-session");

  const mixedHeader = [
    cookieHeader(DEFAULT_SESSION_COOKIE_NAME, login.wireValue),
    `${DEFAULT_SESSION_COOKIE_NAME}=attacker-session`,
  ].join("; ");

  const rawAttack = sessions.authenticate(mixedHeader, parentContext);
  assert.equal(rawAttack.authenticated, true);
  assert.equal(rawAttack.sessionId, "user-session");
  assert.ok(
    rawAttack.reception.rejected.some(
      (cookie) =>
        cookie.name === DEFAULT_SESSION_COOKIE_NAME &&
        cookie.reason === "invalid-signature",
    ),
  );

  const evilHostOnly = sessions.cookies.issue(
    {
      name: DEFAULT_SESSION_COOKIE_NAME,
      value: "evil-session",
      path: "/",
      secure: true,
      httpOnly: true,
      expires: NOW + HOUR,
    },
    childContext,
  );

  const signedAttack = sessions.authenticate(
    [
      cookieHeader(DEFAULT_SESSION_COOKIE_NAME, login.wireValue),
      cookieHeader(DEFAULT_SESSION_COOKIE_NAME, evilHostOnly.wireValue),
    ].join("; "),
    parentContext,
  );
  assert.equal(signedAttack.authenticated, true);
  assert.equal(signedAttack.sessionId, "user-session");
  assert.ok(
    signedAttack.reception.rejected.some(
      (cookie) =>
        cookie.name === DEFAULT_SESSION_COOKIE_NAME &&
        cookie.reason === "host-only-issuer-mismatch",
    ),
  );
});

test("a parent-domain session survives a sibling subdomain Domain cookie", () => {
  const parent = new CookieManager({ secret: SECRET, now: () => NOW });
  const parentSession = parent.issue(
    {
      name: "sid",
      value: "parent-user",
      domain: ".example.com",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Lax",
      expires: NOW + HOUR,
      created: NOW,
      session: true,
    },
    parentContext,
  );

  assert.throws(
    () =>
      parent.issue(
        {
          name: "sid",
          value: "evil-user",
          domain: ".example.com",
          path: "/",
          secure: true,
          httpOnly: true,
          expires: NOW + HOUR,
          created: NOW - 10_000,
          session: true,
        },
        childContext,
      ),
    (error: unknown) => hasCode(error, "SESSION_ISSUER_MISMATCH"),
  );

  const attacker = new CookieManager({ secret: SECRET, now: () => NOW });
  const evilSession = attacker.issue(
    {
      name: "sid",
      value: "evil-user",
      domain: ".example.com",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Lax",
      expires: NOW + HOUR,
      created: NOW - 10_000,
      session: false,
    },
    childContext,
  );

  const reception = parent.receive(
    [
      cookieHeader("sid", parentSession.wireValue),
      cookieHeader("sid", evilSession.wireValue),
    ].join("; "),
    parentContext,
  );

  const selected = reception.get("sid");
  assert.ok(selected);
  assert.equal(selected.value, "parent-user");
  assert.ok(
    reception.rejected.some(
      (cookie) =>
        cookie.name === "sid" && cookie.reason === "session-revoked",
    ),
  );
});

test("same-name cookies use longest path, then earliest creation", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  const issue = (value: string, path: string, created: number) =>
    manager.issue(
      {
        name: "pref",
        value,
        path,
        secure: true,
        expires: NOW + HOUR,
        created,
      },
      parentContext,
    );

  const account = issue("account", "/account", NOW - 3000);
  const settings = issue("settings", "/account/settings", NOW - 2000);

  const deep = manager.receive(
    [
      cookieHeader("pref", account.wireValue),
      cookieHeader("pref", settings.wireValue),
    ].join("; "),
    { host: "example.com", path: "/account/settings/profile", secure: true },
  );
  assert.equal(deep.get("pref")?.value, "settings");

  const shallow = manager.receive(
    [
      cookieHeader("pref", account.wireValue),
      cookieHeader("pref", settings.wireValue),
    ].join("; "),
    { host: "example.com", path: "/account/profile", secure: true },
  );
  assert.equal(shallow.get("pref")?.value, "account");

  const older = issue("older", "/", NOW - 5000);
  const newer = issue("newer", "/", NOW - 1000);
  const tied = manager.receive(
    [
      cookieHeader("pref", newer.wireValue),
      cookieHeader("pref", older.wireValue),
    ].join("; "),
    { host: "example.com", path: "/", secure: true },
  );
  assert.equal(tied.get("pref")?.value, "older");
});

test("path matching respects segment boundaries and sibling overlap", () => {
  assert.equal(pathMatches("/admin", "/admin"), true);
  assert.equal(pathMatches("/admin/users", "/admin"), true);
  assert.equal(pathMatches("/administrator", "/admin"), false);
  assert.equal(pathMatches("/admin", "/admin/"), false);
  assert.equal(pathMatches("/admin/users", "/admin/"), true);

  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  const admin = manager.issue(
    {
      name: "area",
      value: "admin",
      path: "/admin",
      secure: true,
      expires: NOW + HOUR,
      created: NOW - 2000,
    },
    parentContext,
  );
  const adminSlash = manager.issue(
    {
      name: "area",
      value: "admin-slash",
      path: "/admin/",
      secure: true,
      expires: NOW + HOUR,
      created: NOW - 1000,
    },
    parentContext,
  );

  const sibling = manager.receive(
    [
      cookieHeader("area", admin.wireValue),
      cookieHeader("area", adminSlash.wireValue),
    ].join("; "),
    { host: "example.com", path: "/admin/users", secure: true },
  );
  assert.equal(sibling.get("area")?.value, "admin-slash");

  const exact = manager.receive(
    cookieHeader("area", admin.wireValue),
    { host: "example.com", path: "/admin", secure: true },
  );
  assert.equal(exact.get("area")?.value, "admin");

  const lookalike = manager.receive(
    cookieHeader("area", admin.wireValue),
    { host: "example.com", path: "/administrator", secure: true },
  );
  assert.equal(lookalike.get("area"), undefined);
  assert.equal(lookalike.rejected[0].reason, "path-mismatch");
});

test("expired cookies are rejected", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  const expired = manager.issue(
    {
      name: "old",
      value: "stale",
      path: "/",
      secure: true,
      expires: NOW - 1,
      created: NOW - HOUR,
    },
    parentContext,
  );
  const reception = manager.receive(
    cookieHeader("old", expired.wireValue),
    parentContext,
  );
  assert.equal(reception.get("old"), undefined);
  assert.equal(reception.rejected[0].reason, "expired");
});

test("login rotation invalidates the previous session identifier immediately", () => {
  const sessions = new SessionManager({ secret: SECRET, now: () => NOW });
  const first = sessions.login(parentContext, "first-session");

  const initial = sessions.authenticate(
    cookieHeader(DEFAULT_SESSION_COOKIE_NAME, first.wireValue),
    parentContext,
  );
  assert.equal(initial.authenticated, true);
  assert.equal(initial.sessionId, "first-session");

  const second = sessions.rotate(parentContext, "second-session");
  assert.notEqual(second.wireValue, first.wireValue);

  const oldResult = sessions.authenticate(
    cookieHeader(DEFAULT_SESSION_COOKIE_NAME, first.wireValue),
    parentContext,
  );
  assert.equal(oldResult.authenticated, false);
  assert.equal(oldResult.reason, "session-revoked");

  const newResult = sessions.authenticate(
    cookieHeader(DEFAULT_SESSION_COOKIE_NAME, second.wireValue),
    parentContext,
  );
  assert.equal(newResult.authenticated, true);
  assert.equal(newResult.sessionId, "second-session");
});

test("same-domain logout clears the cookie and revokes the active session", () => {
  const sessions = new SessionManager({ secret: SECRET, now: () => NOW });
  const login = sessions.login(parentContext, "logout-me");

  const cleared = sessions.logout(parentContext);
  assert.match(cleared.header, /Max-Age=0/);
  assert.match(cleared.header, /Expires=Thu, 01 Jan 1970 00:00:00 GMT/);
  assert.match(cleared.header, /Path=\//);
  assert.match(cleared.header, /Secure/);
  assert.doesNotMatch(cleared.header, /Domain=/);

  const oldResult = sessions.authenticate(
    cookieHeader(DEFAULT_SESSION_COOKIE_NAME, login.wireValue),
    parentContext,
  );
  assert.equal(oldResult.authenticated, false);
  assert.equal(oldResult.reason, "session-revoked");

  const relogin = sessions.login(parentContext, "after-logout");
  const reloginResult = sessions.authenticate(
    cookieHeader(DEFAULT_SESSION_COOKIE_NAME, relogin.wireValue),
    parentContext,
  );
  assert.equal(reloginResult.authenticated, true);
  assert.equal(reloginResult.sessionId, "after-logout");
});

test("a legitimate Domain-scoped logout clears the parent-domain cookie", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  const login = manager.issue(
    {
      name: "sid",
      value: "domain-session",
      domain: ".example.com",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Lax",
      expires: NOW + HOUR,
      session: true,
    },
    parentContext,
  );

  const cleared = manager.logout(
    "sid",
    parentContext,
    {
      domain: ".example.com",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Lax",
    },
  );
  assert.match(cleared.header, /Domain=example\.com/);
  assert.match(cleared.header, /Max-Age=0/);

  const oldResult = manager.receive(
    cookieHeader("sid", login.wireValue),
    parentContext,
  );
  assert.equal(oldResult.get("sid"), undefined);
  assert.equal(oldResult.rejected[0].reason, "session-revoked");
});

test("a sibling subdomain cannot log out the parent-domain session", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  manager.issue(
    {
      name: "sid",
      value: "parent-user",
      domain: ".example.com",
      path: "/",
      secure: true,
      httpOnly: true,
      expires: NOW + HOUR,
      session: true,
    },
    parentContext,
  );

  assert.throws(
    () =>
      manager.logout(
        "sid",
        childContext,
        {
          domain: ".example.com",
          path: "/",
          secure: true,
          httpOnly: true,
          sameSite: "Lax",
        },
      ),
    (error: unknown) => hasCode(error, "SESSION_ISSUER_MISMATCH"),
  );
});

test("the Cookie header parser preserves duplicate names and order", () => {
  const parsed = parseCookieHeader(
    " a=1; b = two ; a=3; empty=; quoted=\"x;y\"",
  );
  assert.deepEqual(
    parsed.map(({ name, value }) => ({ name, value })),
    [
      { name: "a", value: "1" },
      { name: "b", value: "two" },
      { name: "a", value: "3" },
      { name: "empty", value: "" },
      { name: "quoted", value: "x;y" },
    ],
  );
});

test("RequestLike objects can provide the host, path and Cookie header", () => {
  const manager = new CookieManager({ secret: SECRET, now: () => NOW });
  const issued = manager.issue(
    {
      name: "pref",
      value: "blue",
      path: "/",
      secure: true,
      expires: NOW + HOUR,
    },
    { host: "example.com", path: "/", secure: true },
  );

  const request = {
    url: "https://EXAMPLE.com:443/account",
    headers: {
      Cookie: cookieHeader("pref", issued.wireValue),
    },
  };
  const reception = manager.receive(request);
  assert.equal(reception.get("pref")?.value, "blue");
});
