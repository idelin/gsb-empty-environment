import { test } from 'node:test';
import assert from 'node:assert/strict';

import { TopicsHost, type BrowsingTopicsCallerContext, type Observation } from '../src/topics-host.ts';
import { installBrowsingTopics, resolveTopicsPolicy, type FrameContext } from '../src/document.ts';
import { createDeterministicRng } from '../src/rng.ts';

const ADVERTISER_A = 'https://ads-a.example';
const ADVERTISER_B = 'https://ads-b.example';
const NEWS_SITE = 'https://news.example';
const SHOP_SITE = 'https://shop.example';

function makeHost(seeds: string[], overrides: Record<string, number> = {}): TopicsHost {
  let cursor = 0;
  const host = new TopicsHost({
    filterProbability: 0,
    randomTopicProbability: 0,
    now: () => 0,
    randomSeed: () => seeds[Math.min(cursor++, seeds.length - 1)],
    ...overrides,
  });
  return host;
}

function observe(host: TopicsHost, callerOrigin: string, topic: number, topLevelSite = NEWS_SITE): void {
  const record: Observation = { callerOrigin, topic, topLevelSite };
  host.recordObservation(record);
}

function callerContext(callerOrigin: string, topLevelSite = NEWS_SITE): BrowsingTopicsCallerContext {
  return { callerOrigin, topLevelSite, topicsPolicyAllowed: true };
}

test('caller never observing a topic gets an empty result', async () => {
  const host = makeHost(['seed-epoch-1']);
  observe(host, ADVERTISER_A, 1);
  observe(host, ADVERTISER_A, 2);
  host.advanceEpoch();

  const topics = await host.fetchTopics(callerContext(ADVERTISER_B));
  assert.deepEqual(topics, []);
});

test('only topics observed by the caller in that epoch are returned', async () => {
  const host = makeHost(['seed-epoch-1']);
  observe(host, ADVERTISER_A, 1);
  observe(host, ADVERTISER_A, 2);
  observe(host, ADVERTISER_B, 3);
  host.advanceEpoch();

  const topicsA = await host.fetchTopics(callerContext(ADVERTISER_A));
  const topicsB = await host.fetchTopics(callerContext(ADVERTISER_B));

  assert.deepEqual(topicsA.map((entry) => entry.topic), [1]);
  assert.deepEqual(topicsB.map((entry) => entry.topic), [3]);
  assert.ok(!topicsA.some((entry) => entry.topic === 3));
  assert.ok(!topicsB.some((entry) => entry.topic === 1 || entry.topic === 2));
});

test('repeated calls return identical results despite random noise', async () => {
  const host = makeHost(['seed-epoch-1', 'seed-epoch-2', 'seed-epoch-3'], {
    filterProbability: 0,
    randomTopicProbability: 0.5,
  });
  observe(host, ADVERTISER_A, 11);
  observe(host, ADVERTISER_A, 22);
  observe(host, ADVERTISER_A, 33);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 44);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 55);
  observe(host, ADVERTISER_A, 66);
  host.advanceEpoch();

  const first = await host.fetchTopics(callerContext(ADVERTISER_A));
  const second = await host.fetchTopics(callerContext(ADVERTISER_A));
  const third = await host.fetchTopics(callerContext(ADVERTISER_A));

  assert.equal(first.length, 3);
  assert.deepEqual(first, second);
  assert.deepEqual(second, third);
  for (const entry of first) {
    assert.ok([11, 22, 33, 44, 55, 66].includes(entry.topic));
  }
});

test('draw is reproducible across host instances sharing epoch seeds', async () => {
  function buildHost(): TopicsHost {
    const host = makeHost(['seed-epoch-1'], { filterProbability: 0.05, randomTopicProbability: 0.5 });
    observe(host, ADVERTISER_A, 11);
    observe(host, ADVERTISER_A, 22);
    observe(host, ADVERTISER_A, 33);
    host.advanceEpoch();
    return host;
  }

  const first = await buildHost().fetchTopics(callerContext(ADVERTISER_A));
  const second = await buildHost().fetchTopics(callerContext(ADVERTISER_A));
  assert.deepEqual(first, second);
});

test('one topic per epoch is returned and at most three topics total', async () => {
  const host = makeHost(['s1', 's2', 's3', 's4']);
  observe(host, ADVERTISER_A, 1);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 2);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 3);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 4);
  host.advanceEpoch();

  const topics = await host.fetchTopics(callerContext(ADVERTISER_A));
  assert.deepEqual(topics.map((entry) => entry.topic), [2, 3, 4]);
  assert.ok(topics.length <= 3);
});

test('epochs older than the last three are pruned', async () => {
  const host = makeHost(['s1', 's2', 's3', 's4', 's5']);
  observe(host, ADVERTISER_A, 1);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 2);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 3);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 4);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 5);
  host.advanceEpoch();

  const topics = await host.fetchTopics(callerContext(ADVERTISER_A));
  assert.deepEqual(topics.map((entry) => entry.topic), [3, 4, 5]);
});

test('in-progress epoch is not returned until it closes', async () => {
  const host = makeHost(['s1', 's2']);
  observe(host, ADVERTISER_A, 1);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 2);

  const beforeClose = await host.fetchTopics(callerContext(ADVERTISER_A));
  assert.deepEqual(beforeClose.map((entry) => entry.topic), [1]);

  host.advanceEpoch();
  const afterClose = await host.fetchTopics(callerContext(ADVERTISER_A));
  assert.deepEqual(afterClose.map((entry) => entry.topic), [1, 2]);
});

test('independent epoch seeds keep per-epoch draws uncorrelatable', async () => {
  async function buildWithSeeds(secondSeed: string): Promise<number[]> {
    const host = makeHost(['epoch-one-seed', secondSeed], {
      filterProbability: 0,
      randomTopicProbability: 1,
    });
    observe(host, ADVERTISER_A, 101);
    observe(host, ADVERTISER_A, 102);
    observe(host, ADVERTISER_A, 103);
    host.advanceEpoch();
    observe(host, ADVERTISER_A, 201);
    observe(host, ADVERTISER_A, 202);
    observe(host, ADVERTISER_A, 203);
    host.advanceEpoch();

    const topics = await host.fetchTopics(callerContext(ADVERTISER_A));
    return topics.map((entry) => entry.topic);
  }

  const baseline = await buildWithSeeds('epoch-two-seed');
  const sameSeeds = await buildWithSeeds('epoch-two-seed');
  const changedSecondSeed = await buildWithSeeds('a-completely-different-seed');

  assert.deepEqual(baseline, sameSeeds);
  assert.equal(baseline[0], changedSecondSeed[0]);
  assert.notEqual(baseline[1], changedSecondSeed[1]);

  const rngEpochOne = await createDeterministicRng('epoch-one-seed', NEWS_SITE, ADVERTISER_A);
  const rngEpochTwo = await createDeterministicRng('epoch-two-seed', NEWS_SITE, ADVERTISER_A);
  assert.notEqual(rngEpochOne(), rngEpochTwo());
});

test('random draw is keyed per top-level site', async () => {
  const host = makeHost(['s1'], { filterProbability: 0, randomTopicProbability: 1 });
  observe(host, ADVERTISER_A, 1);
  observe(host, ADVERTISER_A, 2);
  observe(host, ADVERTISER_A, 3);
  host.advanceEpoch();

  const onNews = await host.fetchTopics(callerContext(ADVERTISER_A, NEWS_SITE));
  const onShop = await host.fetchTopics(callerContext(ADVERTISER_A, SHOP_SITE));
  assert.notDeepEqual(onNews, onShop);

  const repeated = await host.fetchTopics(callerContext(ADVERTISER_A, SHOP_SITE));
  assert.deepEqual(onShop, repeated);
});

test('filter probability can suppress an epoch deterministically', async () => {
  const host = makeHost(['s1'], { filterProbability: 1 });
  observe(host, ADVERTISER_A, 1);
  host.advanceEpoch();

  assert.deepEqual(await host.fetchTopics(callerContext(ADVERTISER_A)), []);
  assert.deepEqual(await host.fetchTopics(callerContext(ADVERTISER_A)), []);
});

test('cross-origin iframe without browsing-topics policy gets an empty array', async () => {
  const host = makeHost(['s1']);
  observe(host, ADVERTISER_A, 1);
  observe(host, ADVERTISER_A, 2);
  host.advanceEpoch();

  const deniedFrame: FrameContext = {
    documentOrigin: ADVERTISER_A,
    topLevelSite: NEWS_SITE,
    isTopLevel: false,
    parentOrigin: NEWS_SITE,
    browsingTopicsAllowed: false,
  };
  assert.equal(resolveTopicsPolicy(deniedFrame), false);

  const deniedDocument = installBrowsingTopics({}, host, deniedFrame, ADVERTISER_A);
  assert.deepEqual(await deniedDocument.browsingTopics(), []);

  const allowedFrame: FrameContext = { ...deniedFrame, browsingTopicsAllowed: true };
  assert.equal(resolveTopicsPolicy(allowedFrame), true);
  const allowedDocument = installBrowsingTopics({}, host, allowedFrame, ADVERTISER_A);
  assert.deepEqual((await allowedDocument.browsingTopics()).map((entry) => entry.topic), [1]);
});

test('same-origin iframe and top-level document keep access without extra policy', async () => {
  const host = makeHost(['s1']);
  observe(host, NEWS_SITE, 7);
  host.advanceEpoch();

  const sameOriginFrame: FrameContext = {
    documentOrigin: NEWS_SITE,
    topLevelSite: NEWS_SITE,
    isTopLevel: false,
    parentOrigin: NEWS_SITE,
    browsingTopicsAllowed: false,
  };
  assert.equal(resolveTopicsPolicy(sameOriginFrame), true);

  const topLevelFrame: FrameContext = {
    documentOrigin: NEWS_SITE,
    topLevelSite: NEWS_SITE,
    isTopLevel: true,
    parentOrigin: null,
    browsingTopicsAllowed: false,
  };
  const documentTarget = installBrowsingTopics({}, host, topLevelFrame, NEWS_SITE);
  assert.deepEqual((await documentTarget.browsingTopics()).map((entry) => entry.topic), [7]);
});

test('every returned topic is one the caller observed, including random draws', async () => {
  const host = makeHost(['s1', 's2'], { filterProbability: 0.05, randomTopicProbability: 1 });
  observe(host, ADVERTISER_A, 10);
  observe(host, ADVERTISER_A, 20);
  observe(host, ADVERTISER_B, 99);
  host.advanceEpoch();
  observe(host, ADVERTISER_A, 30);
  observe(host, ADVERTISER_B, 88);
  host.advanceEpoch();

  const topics = await host.fetchTopics(callerContext(ADVERTISER_A));
  for (const entry of topics) {
    assert.ok([10, 20, 30].includes(entry.topic));
    assert.equal(entry.version, 'chrome.1');
  }
});
