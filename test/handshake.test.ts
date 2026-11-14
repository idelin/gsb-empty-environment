import assert from "node:assert/strict";
import { after, before, describe, it } from "node:test";
import { createMessageService, type MessageService } from "../src/message-service.ts";
import { createSocketPair, httpRequest } from "./helpers.ts";
import { WsClient, type HandshakeResponse } from "./ws-client.ts";

const ORIGIN = "https://chat.example.com";
const EVIL_ORIGIN = "https://evil.example.net";

let service: MessageService;

async function login(userId: string, role: "user" | "admin" = "user"): Promise<string> {
  const res = await httpRequest(service.server, {
    method: "POST",
    path: "/login",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ userId, role }),
  });
  assert.equal(res.status, 200);
  const setCookie = res.headers["set-cookie"] ?? "";
  assert.match(setCookie, /SameSite=Strict/);
  assert.match(setCookie, /HttpOnly/);
  return (res.json() as { sessionId: string }).sessionId;
}

function adminCall(path: string, adminSid: string, body: unknown) {
  return httpRequest(service.server, {
    method: "POST",
    path,
    headers: { "content-type": "application/json", cookie: `sid=${adminSid}` },
    body: JSON.stringify(body),
  });
}

async function attemptHandshake(options: {
  origin?: string;
  cookie?: string;
  protocols?: readonly string[];
}): Promise<{ response: HandshakeResponse; client: WsClient | null }> {
  const pair = createSocketPair();
  const handshakePromise = WsClient.handshake(pair.client, options);
  // Drain the client's raw HTTP upgrade request from the server side, like
  // node's HTTP parser would, before dispatching the upgrade event.
  await new Promise<void>((resolve) => {
    let received = Buffer.alloc(0);
    const drain = (chunk: Buffer): void => {
      received = Buffer.concat([received, chunk]);
      if (received.includes("\r\n\r\n")) {
        pair.server.off("data", drain);
        resolve();
      }
    };
    pair.server.on("data", drain);
  });
  const req = {
    method: "GET",
    url: "/chat",
    headers: {
      upgrade: "websocket",
      ...(options.origin !== undefined ? { origin: options.origin } : {}),
      ...(options.cookie !== undefined ? { cookie: options.cookie } : {}),
      ...(options.protocols !== undefined
        ? { "sec-websocket-protocol": options.protocols.join(", ") }
        : {}),
      "sec-websocket-key": Buffer.from("0123456789abcdef").toString("base64"),
      "sec-websocket-version": "13",
    },
  };
  service.server.emit("upgrade", req, pair.server, Buffer.alloc(0));
  return handshakePromise;
}

before(() => {
  service = createMessageService({
    allowedOrigins: [ORIGIN],
    supportedProtocols: ["chat.v1"],
  });
});

after(async () => {
  await service.close();
});

describe("websocket handshake authentication", () => {
  it("accepts a legitimate same-site connection and echoes with bound context", async () => {
    const sid = await login("alice");
    const { response, client } = await attemptHandshake({
      origin: ORIGIN,
      cookie: `sid=${sid}`,
      protocols: ["chat.v1"],
    });
    assert.equal(response.status, 101);
    assert.equal(response.headers.get("sec-websocket-protocol"), "chat.v1");
    assert.ok(response.headers.get("sec-websocket-accept"));
    assert.ok(client);

    client.send("hello");
    const reply = JSON.parse(await client.nextMessage()) as Record<string, unknown>;
    assert.equal(reply.from, "alice");
    assert.equal(reply.role, "user");
    assert.equal(reply.generation, 1);
    assert.equal(reply.echo, "hello");
    client.close();
  });

  it("rejects a cross-site handshake even with a valid victim cookie", async () => {
    const sid = await login("victim");
    const { response, client } = await attemptHandshake({
      origin: EVIL_ORIGIN,
      cookie: `sid=${sid}`,
      protocols: ["chat.v1"],
    });
    assert.equal(response.status, 403);
    assert.equal(client, null);
    assert.match(response.body, /origin not allowed/);
  });

  it("rejects a handshake with an empty Origin header", async () => {
    const sid = await login("noorigin");
    const { response, client } = await attemptHandshake({
      cookie: `sid=${sid}`,
      protocols: ["chat.v1"],
    });
    assert.equal(response.status, 403);
    assert.equal(client, null);
    assert.match(response.body, /missing Origin/);
  });

  it("rejects subprotocol substitution with an unsupported protocol", async () => {
    const sid = await login("protoswap");
    const { response, client } = await attemptHandshake({
      origin: ORIGIN,
      cookie: `sid=${sid}`,
      protocols: ["chat.v2-beta"],
    });
    assert.equal(response.status, 400);
    assert.equal(client, null);
    assert.match(response.body, /subprotocol/);
  });

  it("rejects a reused cookie after the session is revoked", async () => {
    const adminSid = await login("root", "admin");
    const sid = await login("revoked-user");
    const revoked = await adminCall("/admin/revoke", adminSid, { userId: "revoked-user" });
    assert.equal(revoked.status, 200);

    const { response, client } = await attemptHandshake({
      origin: ORIGIN,
      cookie: `sid=${sid}`,
      protocols: ["chat.v1"],
    });
    assert.equal(response.status, 401);
    assert.equal(client, null);

    const whoami = await httpRequest(service.server, {
      method: "GET",
      path: "/whoami",
      headers: { cookie: `sid=${sid}` },
    });
    assert.equal(whoami.status, 401);
  });

  it("closes live connections when permissions change and enforces the new role on HTTP", async () => {
    const adminSid = await login("root2", "admin");
    const sid = await login("demoted", "admin");

    const { response, client } = await attemptHandshake({
      origin: ORIGIN,
      cookie: `sid=${sid}`,
      protocols: ["chat.v1"],
    });
    assert.equal(response.status, 101);
    assert.ok(client);

    const before = await httpRequest(service.server, {
      method: "GET",
      path: "/whoami",
      headers: { cookie: `sid=${sid}` },
    });
    assert.equal((before.json() as { role: string }).role, "admin");

    const demote = await adminCall("/admin/set-role", adminSid, { userId: "demoted", role: "user" });
    assert.equal(demote.status, 200);

    const closeInfo = await client.waitForClose();
    assert.equal(closeInfo.code, 4001);

    const after = await httpRequest(service.server, {
      method: "GET",
      path: "/whoami",
      headers: { cookie: `sid=${sid}` },
    });
    const identity = after.json() as { role: string; generation: number };
    assert.equal(identity.role, "user");
    assert.equal(identity.generation, 2);

    const denied = await httpRequest(service.server, {
      method: "POST",
      path: "/admin/revoke",
      headers: { "content-type": "application/json", cookie: `sid=${sid}` },
      body: JSON.stringify({ userId: "someone" }),
    });
    assert.equal(denied.status, 403);
  });
});
