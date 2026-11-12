import { test } from "node:test";
import assert from "node:assert/strict";

import {
  opaqueOrigin,
  sameOrigin,
  tupleOrigin,
  type Origin,
} from "../index.ts";
import { openDocument } from "../src/sandbox.ts";
import { SecureMessageHost, type SecureMessage } from "../src/messaging.ts";

function message(data: string, senderOrigin: Origin): SecureMessage<string> {
  return { data, senderOrigin };
}

test("消息宿主按结构化来源投递：默认端口等价、跨域拒绝", () => {
  const trustedApp = tupleOrigin("https", "app.example.test");
  const host = new SecureMessageHost<string>([trustedApp]);

  const fromDefaultPort = host.receive(
    message("implicit-443", tupleOrigin("https", "app.example.test", 443)),
  );
  const fromExplicitDefault = host.receive(
    message("explicit-443", tupleOrigin("HTTPS", "APP.example.TEST")),
  );
  const fromOtherPort = host.receive(
    message("port-8443", tupleOrigin("https", "app.example.test", 8443)),
  );
  const fromOtherHost = host.receive(
    message("evil", tupleOrigin("https", "evil.example.test")),
  );

  assert.equal(fromDefaultPort.accepted, true);
  assert.equal(fromExplicitDefault.accepted, true);
  assert.equal(fromOtherPort.accepted, false);
  assert.equal(fromOtherHost.accepted, false);
  assert.deepEqual(host.acceptedMessages.map((m) => m.data), [
    "implicit-443",
    "explicit-443",
  ]);
  assert.deepEqual(
    host.rejectedMessages.map((m) => m.reason),
    ["origin-mismatch", "origin-mismatch"],
  );
});

test("沙箱消息不串域：同身份不透明来源通过，独立沙箱即使显示 null 也被拒", () => {
  const sandboxRoot = openDocument({
    url: "https://app.example.test/frame",
    sandboxed: true,
  });
  const sandboxChild = openDocument({
    url: "about:blank",
    sandboxed: true,
    opener: sandboxRoot,
  });
  const foreignSandbox = openDocument({
    url: "https://evil.example.test/frame",
    sandboxed: true,
  });

  assert.ok(sameOrigin(sandboxRoot.origin, sandboxChild.origin));
  assert.equal(false, sameOrigin(sandboxRoot.origin, foreignSandbox.origin));

  const host = new SecureMessageHost<string>([sandboxRoot.origin]);

  const internal = host.receive(message("sandbox-internal", sandboxChild.origin));
  const foreign = host.receive(message("sandbox-escape", foreignSandbox.origin));
  const extraOpaque = host.receive(message("fresh-opaque", opaqueOrigin()));

  assert.equal(internal.accepted, true);
  assert.equal(foreign.accepted, false);
  assert.equal(extraOpaque.accepted, false);

  assert.equal(internal.accepted ? internal.message.senderOriginDisplay : "", "null");
  assert.equal(
    foreign.accepted === false ? foreign.rejection.senderOriginDisplay : "",
    "null",
  );
  assert.equal(host.acceptedMessages.length, 1);
  assert.equal(host.rejectedMessages.length, 2);
});

test("普通来源与不透明来源之间永远不串域", () => {
  const app = tupleOrigin("https", "app.example.test", 443);
  const sandbox = opaqueOrigin();
  const host = new SecureMessageHost<string>([app, sandbox]);

  assert.equal(host.receive(message("from-app", app)).accepted, true);
  assert.equal(host.receive(message("from-sandbox", sandbox)).accepted, true);
  assert.equal(host.receive(message("from-other-sandbox", opaqueOrigin())).accepted, false);
  assert.equal(
    host.receive(message("from-lookalike", tupleOrigin("https", "null", 443))).accepted,
    false,
  );
});
