import assert from "node:assert/strict";
import { test } from "node:test";
import {
  TestBrowserHost,
  storagePartition,
  firstPartyPartition,
  samePartition,
} from "../index.ts";

test("partition key binds both origin and top-level site", () => {
  const adOnA = storagePartition("https://ads.example/frame", "https://a.example");
  const adOnB = storagePartition("https://ads.example/frame", "https://b.example");
  const otherOnA = storagePartition("https://other.example/frame", "https://a.example");

  assert.notEqual(adOnA.key, adOnB.key);
  assert.notEqual(adOnA.key, otherOnA.key);
  assert.equal(samePartition(adOnA, storagePartition("https://ads.example/x", "https://a.example")), true);
  assert.equal(firstPartyPartition("https://a.example").topLevelSite.siteHost, "a.example");
});

test("nested sites: same-name channels under different top-level sites never cross", async () => {
  const host = new TestBrowserHost();
  const topA = host.openFrame("doc-a", "https://a.example");
  const topB = host.openFrame("doc-b", "https://b.example");
  const adOnA = host.openFrame("doc-ad-a", "https://ads.example/widget", "https://a.example");
  const adOnB = host.openFrame("doc-ad-b", "https://ads.example/widget", "https://b.example");

  const gotA: unknown[] = [];
  const gotB: unknown[] = [];
  adOnA.createChannel<string>("sync").addListener((m) => gotA.push(m.data));
  adOnB.createChannel<string>("sync").addListener((m) => gotB.push(m.data));

  topA.createChannel<string>("sync").postMessage("from-a");
  topB.createChannel<string>("sync").postMessage("from-b");
  await host.flush();

  assert.deepEqual(gotA, []);
  assert.deepEqual(gotB, []);

  adOnA.createChannel<string>("sync").postMessage("ad-on-a");
  adOnB.createChannel<string>("sync").postMessage("ad-on-b");
  await host.flush();

  assert.deepEqual(gotA, ["ad-on-a"]);
  assert.deepEqual(gotB, ["ad-on-b"]);
});

test("same-name channels in the same partition do receive each other", async () => {
  const host = new TestBrowserHost();
  const frame1 = host.openFrame("doc-1", "https://ads.example/w", "https://a.example");
  const frame2 = host.openFrame("doc-2", "https://ads.example/w", "https://a.example");

  const inbox: unknown[] = [];
  frame2.createChannel<string>("bus").addListener((m) => inbox.push(m.data));
  frame1.createChannel<string>("bus").postMessage("hello-partition");
  await host.flush();

  assert.deepEqual(inbox, ["hello-partition"]);
});

test("same-name channels with different names never collide", async () => {
  const host = new TestBrowserHost();
  const frame1 = host.openFrame("doc-x", "https://a.example");
  const frame2 = host.openFrame("doc-y", "https://a.example");

  const alpha: unknown[] = [];
  const beta: unknown[] = [];
  frame2.createChannel<string>("alpha").addListener((m) => alpha.push(m.data));
  frame2.createChannel<string>("beta").addListener((m) => beta.push(m.data));
  frame1.createChannel<string>("alpha").postMessage("a");
  frame1.createChannel<string>("beta").postMessage("b");
  await host.flush();

  assert.deepEqual(alpha, ["a"]);
  assert.deepEqual(beta, ["b"]);
});

test("sender never receives its own message", async () => {
  const host = new TestBrowserHost();
  const frame = host.openFrame("doc-self", "https://a.example");
  const channel = frame.createChannel<string>("self");
  const seen: unknown[] = [];
  channel.addListener((m) => seen.push(m.data));
  channel.postMessage("to-myself");
  await host.flush();

  assert.deepEqual(seen, []);
});

test("third-party nested site isolation matrix across two top-level sites", async () => {
  const host = new TestBrowserHost();
  const adsOnA = host.openFrame("m-ads-a", "https://ads.example/embed", "https://a.example");
  const adsOnB = host.openFrame("m-ads-b", "https://ads.example/embed", "https://b.example");
  const topA = host.openFrame("m-top-a", "https://a.example");
  const topB = host.openFrame("m-top-b", "https://b.example");

  const matrix: Record<string, unknown[]> = {
    adsOnA: [],
    adsOnB: [],
    topA: [],
    topB: [],
  };
  adsOnA.createChannel<string>("m").addListener((e) => matrix.adsOnA.push(e.data));
  adsOnB.createChannel<string>("m").addListener((e) => matrix.adsOnB.push(e.data));
  topA.createChannel<string>("m").addListener((e) => matrix.topA.push(e.data));
  topB.createChannel<string>("m").addListener((e) => matrix.topB.push(e.data));

  adsOnA.createChannel<string>("m").postMessage("from-ads-on-a");
  topA.createChannel<string>("m").postMessage("from-top-a");
  adsOnB.createChannel<string>("m").postMessage("from-ads-on-b");
  topB.createChannel<string>("m").postMessage("from-top-b");
  await host.flush();

  assert.deepEqual(matrix, {
    adsOnA: ["from-ads-on-a"],
    adsOnB: ["from-ads-on-b"],
    topA: ["from-top-a"],
    topB: ["from-top-b"],
  });
});
