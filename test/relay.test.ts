import test from "node:test";
import assert from "node:assert/strict";
import {
  InMemoryRelayStore,
  OneTimeRelay,
  createRelayHandler,
  createReferrerPortal,
} from "../index.ts";

function createNodeResponse() {
  const headers: Record<string, string> = {};
  return {
    statusCode: 200,
    body: "",
    headers,
    setHeader(name: string, value: string) {
      headers[name] = value;
    },
    end(body = "") {
      this.body = body;
    },
  };
}

test("relay links expose only an opaque one-time token", () => {
  const relay = new OneTimeRelay({ baseUrl: "https://portal.example" });
  const link = relay.createLink(
    "https://third.example/callback?reset_token=abc&topic=help",
  );

  assert.ok(link);
  assert.match(link.url, /^https:\/\/portal\.example\/__referrer-relay\?t=/);
  assert.doesNotMatch(
    link.url,
    /third\.example|reset_token|topic=|reset=/,
  );
  assert.equal(
    link.target,
    "https://third.example/callback",
  );
});

test("a relay token can be consumed exactly once", () => {
  const relay = new OneTimeRelay({ baseUrl: "https://portal.example" });
  const link = relay.createLink("https://third.example/callback?token=abc");
  assert.ok(link);

  const first = relay.handleRequest(link.url);
  assert.equal(first.status, 302);
  assert.equal(first.location, "https://third.example/callback");
  assert.equal(first.headers["Referrer-Policy"], "no-referrer");
  assert.equal(first.headers["Cache-Control"], "no-store");

  const second = relay.handleRequest(link.url);
  assert.equal(second.status, 410);
  assert.equal(second.location, undefined);
});

test("relay token is not copied into the final Location", () => {
  const relay = new OneTimeRelay();
  const link = relay.createLink("https://third.example/callback");
  assert.ok(link);
  const result = relay.handleRequest(link.url);

  assert.equal(result.status, 302);
  assert.doesNotMatch(result.location ?? "", new RegExp(link.token));
  assert.doesNotMatch(result.location ?? "", /[?&]t=/);
});

test("expired relay tokens are rejected and removed", () => {
  let now = 1_000;
  const store = new InMemoryRelayStore({
    ttlMs: 50,
    now: () => now,
  });
  const relay = new OneTimeRelay({ store });
  const link = relay.createLink("https://third.example/callback", {
    ttlMs: 10,
  });
  assert.ok(link);
  now = 1_011;

  const result = relay.handleRequest(link.url);
  assert.equal(result.status, 410);
  assert.equal(store.size, 0);
});

test("relay rejects unsafe destinations and keeps only safe query params", () => {
  const relay = new OneTimeRelay();

  assert.equal(relay.createLink("javascript:alert(1)"), null);
  assert.equal(relay.createLink("data:text/html,alert(1)"), null);

  const link = relay.createLink(
    "https://third.example/path?token=abc&safe=1",
  );
  assert.ok(link);
  assert.equal(link.target, "https://third.example/path");
});

test("relay preserves safe query parameters when no token is present", () => {
  const relay = new OneTimeRelay();
  const link = relay.createLink(
    "https://third.example/path?topic=help&utm_source=portal",
  );
  assert.ok(link);
  assert.equal(
    link.target,
    "https://third.example/path?topic=help&utm_source=portal",
  );
});

test("missing relay tokens return 400 without a redirect", () => {
  const relay = new OneTimeRelay();
  const result = relay.handleRequest("/__referrer-relay");
  assert.equal(result.status, 400);
  assert.equal(result.headers.Location, undefined);
});

test("Node relay handler writes a one-time redirect response", () => {
  const relay = new OneTimeRelay();
  const link = relay.createLink("https://third.example/callback");
  assert.ok(link);

  const handler = createRelayHandler(relay);
  const req = { url: link.url, method: "GET" };
  const res = createNodeResponse();

  assert.equal(handler(req, res), true);
  assert.equal(res.statusCode, 302);
  assert.equal(res.headers.Location, "https://third.example/callback");
  assert.equal(res.headers["Referrer-Policy"], "no-referrer");

  const replay = createNodeResponse();
  assert.equal(handler(req, replay), true);
  assert.equal(replay.statusCode, 410);
  assert.equal(replay.headers.Location, undefined);
});

test("portal relay route consumes the token and emits no sensitive referrer", () => {
  const portal = createReferrerPortal({
    baseUrl: "https://portal.example",
  });
  assert.ok(portal.relay);

  const link = portal.relay.createLink(
    "https://third.example/callback?reset_token=abc&topic=help",
  );
  assert.ok(link);

  const req = { url: link.url, method: "GET" };
  const res = createNodeResponse();
  assert.equal(portal.handle(req, res), true);
  assert.equal(res.statusCode, 302);
  assert.equal(res.headers.Location, "https://third.example/callback");
  assert.equal(res.headers["Referrer-Policy"], "no-referrer");

  const replay = createNodeResponse();
  assert.equal(portal.handle(req, replay), true);
  assert.equal(replay.statusCode, 410);
});
