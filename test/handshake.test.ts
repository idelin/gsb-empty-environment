import assert from "node:assert/strict";
import { describe, test } from "node:test";

import {
  SEC_WEBSOCKET_PROTOCOL,
  UNAUTHORIZED_MESSAGE_CLOSE_CODE,
  UNAUTHORIZED_MESSAGE_REASON,
  closeCodeForMessageType,
  createSubprotocolPolicy,
  enforceMessageType,
  handshakeLogRecord,
  isMessageTypeAllowed,
  negotiateHandshake,
  negotiateSubprotocol,
  parseSubprotocolHeader,
  shouldCloseForMessageType,
} from "../index.ts";
import type {
  ClosableWebSocket,
  SafeHandshakeLogRecord,
} from "../index.ts";

const policy = createSubprotocolPolicy([
  { name: "chat.v2", messageTypes: ["chat.message", "chat.typing"] },
  { name: "chat.v1", messageTypes: ["chat.message"] },
]);

describe("subprotocol negotiation", () => {
  test("accepts without a subprotocol when there is no intersection", () => {
    const result = negotiateHandshake({
      header: "chat.v3, metrics.v1",
      protocols: policy,
    });

    assert.equal(result.accepted, true);
    assert.equal(result.statusCode, 101);
    assert.equal(result.subprotocol, null);
    assert.equal(result.hasSubprotocol, false);
    assert.deepEqual(result.responseHeaders, {});
    assert.deepEqual(result.allowedMessageTypes, []);
    assert.equal(result.error, null);
    assert.equal(
      Object.keys(result.responseHeaders).some(
        (name) => name.toLowerCase() === "sec-websocket-protocol",
      ),
      false,
    );
    assert.equal(shouldCloseForMessageType(result, "chat.message"), true);
  });

  test("selects exactly one protocol using server priority and trims OWS", () => {
    const result = negotiateHandshake({
      header: "\tchat.v1 ,  chat.v2\t",
      protocols: policy,
    });

    assert.equal(result.accepted, true);
    assert.equal(result.subprotocol, "chat.v2");
    assert.equal(result.hasSubprotocol, true);
    assert.equal(
      result.responseHeaders[SEC_WEBSOCKET_PROTOCOL],
      "chat.v2",
    );
    assert.deepEqual(result.allowedMessageTypes, [
      "chat.message",
      "chat.typing",
    ]);
    assert.deepEqual(Object.keys(result.responseHeaders), [
      SEC_WEBSOCKET_PROTOCOL,
    ]);
  });

  test("supports repeated header values as one comma-separated list", () => {
    const result = negotiateSubprotocol(["chat.v1", "chat.v2"], policy);

    assert.equal(result.accepted, true);
    assert.equal(result.subprotocol, "chat.v2");
  });

  test("rejects duplicate client subprotocol names", () => {
    const result = negotiateHandshake({
      header: "chat.v2, chat.v1, chat.v2",
      protocols: policy,
    });

    assert.equal(result.accepted, false);
    assert.equal(result.statusCode, 400);
    assert.equal(result.subprotocol, null);
    assert.deepEqual(result.responseHeaders, {});
    assert.equal(result.error?.code, "duplicate_client_protocol");
  });

  test("rejects duplicate bearer elements without exposing their names", () => {
    const secret = "duplicate-secret";
    const result = negotiateHandshake({
      header: `bearer.${secret}, bearer.${secret}`,
      protocols: policy,
    });

    assert.equal(result.accepted, false);
    assert.equal(result.error?.code, "duplicate_client_protocol");
    assert.equal(result.token, null);
    assert.equal(JSON.stringify(result).includes(secret), false);
  });

  test("rejects invalid tokens, empty list elements, and empty bearer values", () => {
    for (const header of [
      "",
      "chat v2",
      "chat/v2",
      "chat.v2,,chat.v1",
      "bearer.",
    ]) {
      const result = negotiateHandshake({ header, protocols: policy });
      assert.equal(result.accepted, false, header);
      assert.equal(result.error?.code, "invalid_client_protocol", header);
    }
  });

  test("extracts a bearer token without echoing it or logging it", () => {
    const secret = "top.secret-token";
    const logs: SafeHandshakeLogRecord[] = [];

    const result = negotiateHandshake({
      header: ` chat.v1 , bearer.${secret} `,
      protocols: policy,
      logger: (record) => logs.push(record),
    });

    assert.equal(result.accepted, true);
    assert.equal(result.subprotocol, "chat.v1");
    assert.equal(result.token, secret);
    assert.equal(result.hasBearerToken, true);
    assert.equal(
      result.responseHeaders[SEC_WEBSOCKET_PROTOCOL],
      "chat.v1",
    );

    const responseText = JSON.stringify(result.responseHeaders);
    const logText = JSON.stringify(logs);
    assert.equal(responseText.includes(secret), false);
    assert.equal(responseText.includes("bearer."), false);
    assert.equal(logText.includes(secret), false);
    assert.equal(logText.includes("bearer."), false);
    assert.equal(logs.length, 1);
    assert.equal(logs[0]?.subprotocol, "chat.v1");
    assert.equal(logs[0]?.hasBearerToken, true);
  });

  test("can extract a token when no subprotocol intersects", () => {
    const result = negotiateHandshake({
      header: "unknown, bearer.token-only",
      protocols: policy,
    });

    assert.equal(result.accepted, true);
    assert.equal(result.subprotocol, null);
    assert.equal(result.token, "token-only");
    assert.deepEqual(result.responseHeaders, {});
  });

  test("never treats a bearer element as a selectable subprotocol", () => {
    const result = negotiateHandshake({
      header: "bearer.chat.v2, chat.v2",
      protocols: policy,
    });

    assert.equal(result.accepted, true);
    assert.equal(result.subprotocol, "chat.v2");
    assert.equal(result.token, "chat.v2");
    assert.equal(
      result.responseHeaders[SEC_WEBSOCKET_PROTOCOL],
      "chat.v2",
    );
  });

  test("rejects multiple bearer tokens as ambiguous", () => {
    const result = negotiateHandshake({
      header: "bearer.first-secret, bearer.second-secret, chat.v1",
      protocols: policy,
    });

    assert.equal(result.accepted, false);
    assert.equal(result.error?.code, "multiple_bearer_tokens");
    assert.equal(result.token, null);
    assert.equal(JSON.stringify(handshakeLogRecord(result)).includes("secret"), false);
  });

  test("parses an absent header as an accepted empty offer", () => {
    const parsed = parseSubprotocolHeader(undefined);

    assert.equal(parsed.ok, true);
    if (parsed.ok) {
      assert.deepEqual(parsed.protocols, []);
      assert.equal(parsed.token, null);
      assert.equal(parsed.offeredProtocolCount, 0);
    }
  });
});

describe("subprotocol message-type authorization", () => {
  test("allows only message types granted by the selected subprotocol", () => {
    const result = negotiateHandshake({
      header: "chat.v1",
      protocols: policy,
    });

    assert.equal(result.subprotocol, "chat.v1");
    assert.equal(isMessageTypeAllowed(result, "chat.message"), true);
    assert.equal(isMessageTypeAllowed(result, "chat.typing"), false);
    assert.equal(isMessageTypeAllowed(result, "admin.broadcast"), false);
    assert.equal(messageCloseCode(result, "chat.message"), null);
    assert.equal(messageCloseCode(result, "admin.broadcast"), 1008);
  });

  test("closes with 1008 for an unauthorized message type", () => {
    const result = negotiateHandshake({
      header: "chat.v1",
      protocols: policy,
    });
    const closes: Array<{ code?: number; reason?: string }> = [];
    const socket: ClosableWebSocket = {
      close(code, reason) {
        closes.push({ code, reason });
      },
    };

    assert.equal(enforceMessageType(result, "chat.message", socket), true);
    assert.deepEqual(closes, []);

    assert.equal(enforceMessageType(result, "admin.broadcast", socket), false);
    assert.deepEqual(closes, [
      {
        code: UNAUTHORIZED_MESSAGE_CLOSE_CODE,
        reason: UNAUTHORIZED_MESSAGE_REASON,
      },
    ]);
    assert.equal(closes[0]?.code, 1008);
    assert.equal(closes[0]?.reason?.includes("admin.broadcast"), false);
  });

  test("closes all message types when no subprotocol was selected", () => {
    const result = negotiateHandshake({
      header: "unsupported",
      protocols: policy,
    });
    const socket: ClosableWebSocket = {
      close() {},
    };

    assert.equal(enforceMessageType(result, "chat.message", socket), false);
    assert.equal(closeCodeForMessageType(result, "chat.message"), 1008);
  });
});

describe("server policy validation", () => {
  test("rejects duplicate or invalid server protocol definitions", () => {
    assert.throws(
      () =>
        createSubprotocolPolicy([
          { name: "chat.v1" },
          { name: "chat.v1" },
        ]),
      /must be unique/,
    );
    assert.throws(
      () => createSubprotocolPolicy([{ name: "chat v1" }]),
      /RFC 6455 token/,
    );
  });

  test("rejects bearer-shaped server protocol names", () => {
    assert.throws(
      () => createSubprotocolPolicy([{ name: "bearer.server-secret" }]),
      /bearer namespace/,
    );
    assert.throws(
      () => createSubprotocolPolicy([{ name: "Bearer.server-secret" }]),
      /bearer namespace/,
    );
  });
});

function messageCloseCode(
  result: ReturnType<typeof negotiateHandshake>,
  messageType: string,
): 1008 | null {
  return closeCodeForMessageType(result, messageType);
}
