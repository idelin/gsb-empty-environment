import assert from "node:assert/strict";
import { test } from "node:test";
import { TestBrowserHost } from "../index.ts";

test("queued messages retain the partition captured at send time", async () => {
  const host = new TestBrowserHost();
  const sender = host.openFrame("doc-sender", "https://ads.example/w", "https://a.example");
  const receiverA = host.openFrame("doc-recv-a", "https://ads.example/w", "https://a.example");
  const receiverB = host.openFrame("doc-recv-b", "https://ads.example/w", "https://b.example");

  const gotA: unknown[] = [];
  const gotB: unknown[] = [];
  receiverA.createChannel<string>("q").addListener((m) => gotA.push(m.data));
  receiverB.createChannel<string>("q").addListener((m) => gotB.push(m.data));

  const senderChannel = sender.createChannel<string>("q");
  senderChannel.postMessage("captured-on-a");
  assert.equal(host.pendingCount, 1);

  host.navigate(sender, { topLevelSite: "https://b.example" });
  assert.equal(senderChannel.isClosed, true);

  await host.flush();
  assert.deepEqual(gotA, ["captured-on-a"]);
  assert.deepEqual(gotB, []);
});

test("close while queued: closed channel is skipped, others still get exactly one delivery", async () => {
  const host = new TestBrowserHost();
  const sender = host.openFrame("doc-s1", "https://a.example");
  const victim = host.openFrame("doc-victim", "https://a.example");
  const survivor = host.openFrame("doc-survivor", "https://a.example");

  const victimInbox: unknown[] = [];
  const survivorInbox: unknown[] = [];
  const victimChannel = victim.createChannel<string>("race");
  victimChannel.addListener((m) => victimInbox.push(m.data));
  const survivorChannel = survivor.createChannel<string>("race");
  survivorChannel.addListener((m) => survivorInbox.push(m.data));

  sender.createChannel<string>("race").postMessage("one-shot");
  victimChannel.close();
  await host.flush();

  assert.deepEqual(victimInbox, []);
  assert.deepEqual(survivorInbox, ["one-shot"]);

  const record = host.delivered[0];
  assert.equal(record.envelope.data, "one-shot");
  assert.ok(!record.recipientIds.includes(victimChannel.id));
  assert.deepEqual(record.recipientIds, [survivorChannel.id]);
});

test("close inside a listener never causes double delivery", async () => {
  const host = new TestBrowserHost();
  const sender = host.openFrame("doc-s2", "https://a.example");
  const frame = host.openFrame("doc-selfclose", "https://a.example");
  const channel = frame.createChannel<string>("race2");
  let calls = 0;
  channel.addListener(() => {
    calls += 1;
    channel.close();
  });

  sender.createChannel<string>("race2").postMessage("boom");
  await host.flush();

  assert.equal(calls, 1);
  assert.equal(channel.isClosed, true);
});

test("each queued envelope is dequeued once even with concurrent flushes", async () => {
  const host = new TestBrowserHost();
  const sender = host.openFrame("doc-s3", "https://a.example");
  const receiver = host.openFrame("doc-recv3", "https://a.example");
  const inbox: unknown[] = [];
  receiver.createChannel<string>("fifo").addListener((m) => inbox.push(m.data));

  const channel = sender.createChannel<string>("fifo");
  channel.postMessage("m1");
  channel.postMessage("m2");
  channel.postMessage("m3");

  await Promise.all([host.flush(), host.flush(), host.flush()]);

  assert.deepEqual(inbox, ["m1", "m2", "m3"]);
  assert.deepEqual(
    host.delivered.map((r) => r.envelope.enqueueOrder),
    [0, 1, 2],
  );
});

test("close is idempotent and safe after delivery", async () => {
  const host = new TestBrowserHost();
  const frame = host.openFrame("doc-idem", "https://a.example");
  const channel = frame.createChannel<string>("idem");
  channel.close();
  channel.close();
  assert.equal(channel.isClosed, true);
  assert.equal(host.pendingCount, 0);
});

test("channel closed by a listener receives no further queued messages", async () => {
  const host = new TestBrowserHost();
  const sender = host.openFrame("doc-s5", "https://a.example");
  const frame = host.openFrame("doc-once", "https://a.example");
  const channel = frame.createChannel<string>("once");
  const received: unknown[] = [];
  channel.addListener((m) => {
    received.push(m.data);
    channel.close();
  });

  const out = sender.createChannel<string>("once");
  out.postMessage("first");
  out.postMessage("second");
  await host.flush();

  assert.deepEqual(received, ["first"]);
  assert.equal(host.delivered[1].recipientIds.length, 0);
});

test("listener failures do not break exactly-once delivery to other recipients", async () => {
  const host = new TestBrowserHost();
  const sender = host.openFrame("doc-s4", "https://a.example");
  const bad = host.openFrame("doc-bad", "https://a.example");
  const good = host.openFrame("doc-good", "https://a.example");

  const goodInbox: unknown[] = [];
  const badChannel = bad.createChannel<string>("err");
  badChannel.addListener(() => {
    throw new Error("listener boom");
  });
  good.createChannel<string>("err").addListener((m) => goodInbox.push(m.data));

  sender.createChannel<string>("err").postMessage("payload");
  await assert.rejects(host.flush(), (error: unknown) => {
    assert.ok(error instanceof AggregateError);
    assert.match((error as AggregateError).errors[0].message, /listener boom/);
    return true;
  });

  assert.deepEqual(goodInbox, ["payload"]);
  assert.equal(host.delivered[0].recipientIds.length, 2);
});
