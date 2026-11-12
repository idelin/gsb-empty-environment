import assert from "node:assert/strict";
import { test } from "node:test";
import { TestBrowserHost } from "../index.ts";

test("navigation invalidates old subscriptions immediately", async () => {
  const host = new TestBrowserHost();
  const frame = host.openFrame("doc-nav", "https://a.example");
  const oldChannel = frame.createChannel<string>("news");
  const oldInbox: unknown[] = [];
  oldChannel.addListener((m) => oldInbox.push(m.data));

  const peer = host.openFrame("doc-peer", "https://a.example");
  const navigated = host.navigate(frame, { origin: "https://a.example", topLevelSite: "https://b.example" });

  assert.equal(oldChannel.isClosed, true);
  assert.notEqual(navigated.document.token, frame.document.token);
  assert.equal(navigated.partition.key === frame.partition.key, false);

  peer.createChannel<string>("news").postMessage("after-nav");
  await host.flush();

  assert.deepEqual(oldInbox, []);

  const newInbox: unknown[] = [];
  const newChannel = navigated.createChannel<string>("news");
  newChannel.addListener((m) => newInbox.push(m.data));
  const peerInB = host.navigate(peer, { topLevelSite: "https://b.example" });
  peerInB.createChannel<string>("news").postMessage("new-partition-only");
  await host.flush();

  assert.deepEqual(oldInbox, []);
  assert.deepEqual(newInbox, ["new-partition-only"]);
});

test("reused document id gets a fresh generation and cannot receive old traffic", async () => {
  const host = new TestBrowserHost();
  const first = host.openFrame("doc-reuse", "https://a.example");
  const firstChannel = first.createChannel<string>("reuse");
  const firstInbox: unknown[] = [];
  firstChannel.addListener((m) => firstInbox.push(m.data));
  const firstToken = first.document.token;

  host.closeFrame(first);
  assert.equal(firstChannel.isClosed, true);

  const second = host.openFrame("doc-reuse", "https://a.example");
  assert.notEqual(second.document.token, firstToken);
  assert.equal(second.docId, first.docId);

  const secondInbox: unknown[] = [];
  second.createChannel<string>("reuse").addListener((m) => secondInbox.push(m.data));

  const peer = host.openFrame("doc-peer-2", "https://a.example");
  peer.createChannel<string>("reuse").postMessage("generation-2");
  await host.flush();

  assert.deepEqual(firstInbox, []);
  assert.deepEqual(secondInbox, ["generation-2"]);
});

test("duplicate live document id is rejected", () => {
  const host = new TestBrowserHost();
  host.openFrame("doc-dup", "https://a.example");
  assert.throws(() => host.openFrame("doc-dup", "https://a.example"), /duplicate live document id/);
});

test("all channels of a document close when the frame closes", async () => {
  const host = new TestBrowserHost();
  const frame = host.openFrame("doc-multi", "https://a.example");
  const c1 = frame.createChannel<string>("c1");
  const c2 = frame.createChannel<string>("c2");
  const peer = host.openFrame("doc-multi-peer", "https://a.example");

  host.closeFrame(frame);
  assert.equal(c1.isClosed, true);
  assert.equal(c2.isClosed, true);
  assert.throws(() => c1.postMessage("x"), /closed/);

  peer.createChannel<string>("c1").postMessage("nobody-home");
  peer.createChannel<string>("c2").postMessage("nobody-home");
  await host.flush();

  const last = host.delivered[host.delivered.length - 1];
  assert.deepEqual(last.recipientIds, []);
});

test("posting after navigation throws because the tracked channel is closed", () => {
  const host = new TestBrowserHost();
  const frame = host.openFrame("doc-post-nav", "https://a.example");
  const channel = frame.createChannel<string>("n");
  host.navigate(frame);
  assert.throws(() => channel.postMessage("late"), /closed/);
});
