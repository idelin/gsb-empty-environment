import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { authorizeInboxActivity } from '../src/index.ts';
import type {
  FetchOutcome,
  InboxActivity,
  InboxSignatureContext,
  ObjectFetcher,
} from '../src/index.ts';

const ALICE = 'https://alice.example/actors/alice';
const ALICE_KEY = `${ALICE}#main-key`;
const BOB = 'https://bob.example/actors/bob';
const BOB_KEY = `${BOB}#main-key`;

function signature(keyId: string): InboxSignatureContext {
  return { keyId };
}

/** 记录调用次数的取回器，默认返回归属为 ALICE 的对象，可按 id 定制响应。 */
function countingFetcher(
  responses: Record<string, FetchOutcome | Promise<FetchOutcome>> = {},
): ObjectFetcher & { calls: string[] } {
  const fetcher = async (objectId: string): Promise<FetchOutcome> => {
    fetcher.calls.push(objectId);
    if (objectId in responses) {
      return responses[objectId];
    }
    return { id: objectId, attributedTo: ALICE };
  };
  fetcher.calls = [] as string[];
  return fetcher;
}

describe('签名 keyId 与活动 actor 一致性', () => {
  it('拒绝用自己的合法签名冒用他人 actor', async () => {
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: 'https://alice.example/notes/1', attributedTo: ALICE },
    };
    const fetcher = countingFetcher();

    const result = await authorizeInboxActivity(activity, {
      signature: signature(BOB_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'signer_actor_mismatch');
    assert.deepEqual(fetcher.calls, []);
  });

  it('keyId 的 fragment 被剥离后与活动 actor 相等时通过', async () => {
    const activity: InboxActivity = {
      type: 'Follow',
      actor: ALICE,
      object: BOB,
    };
    const fetcher = countingFetcher();

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, true);
    assert.equal(result.reason, 'ok');
  });

  it('活动 actor 为对象形式时按其 id 比对', async () => {
    const activity: InboxActivity = {
      type: 'Follow',
      actor: { id: ALICE, type: 'Person' },
      object: BOB,
    };

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: countingFetcher(),
    });

    assert.equal(result.allowed, true);
  });

  it('非 https 的 keyId 被拒绝', async () => {
    const activity: InboxActivity = { type: 'Follow', actor: ALICE, object: BOB };

    const result = await authorizeInboxActivity(activity, {
      signature: signature('http://alice.example/actors/alice#main-key'),
      fetchObject: countingFetcher(),
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'invalid_object_id');
  });
});

describe('Delete / Update 归属校验', () => {
  it('拒绝删除 attributedTo 为他人的内嵌对象', async () => {
    const activity: InboxActivity = {
      type: 'Delete',
      actor: ALICE,
      object: { id: 'https://bob.example/notes/9', attributedTo: BOB },
    };

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: countingFetcher(),
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'attribution_mismatch');
  });

  it('允许删除 attributedTo 为自己的内嵌对象，且不触发取回', async () => {
    const activity: InboxActivity = {
      type: 'Delete',
      actor: ALICE,
      object: { id: 'https://alice.example/notes/1', attributedTo: ALICE },
    };
    const fetcher = countingFetcher();

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, true);
    assert.deepEqual(fetcher.calls, []);
  });

  it('Update 裸 id 目标取回后归属不符则拒绝，且只取回一次', async () => {
    const targetId = 'https://bob.example/notes/9';
    const activity: InboxActivity = { type: 'Update', actor: ALICE, object: targetId };
    const fetcher = countingFetcher({
      [targetId]: { id: targetId, attributedTo: BOB },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'attribution_mismatch');
    assert.deepEqual(fetcher.calls, [targetId]);
    assert.equal(fetcher.calls.length, 1);
  });

  it('Delete 裸 id 目标归属一致时通过并记录取回 id', async () => {
    const targetId = 'https://alice.example/notes/1';
    const activity: InboxActivity = { type: 'Delete', actor: ALICE, object: targetId };
    const fetcher = countingFetcher({
      [targetId]: { id: targetId, attributedTo: ALICE },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, true);
    assert.equal(result.fetchedObjectId, targetId);
  });
});

describe('Create / Announce 外域内嵌对象', () => {
  it('同域内嵌对象不取回，直接按内嵌 attributedTo 放行', async () => {
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: 'https://alice.example/notes/1', attributedTo: ALICE, content: 'hi' },
    };
    const fetcher = countingFetcher();

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, true);
    assert.deepEqual(fetcher.calls, []);
  });

  it('跨域内嵌对象忽略内嵌副本，从对象 id 重新取回权威副本', async () => {
    const foreignId = 'https://bob.example/notes/9';
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      // 攻击者在投递里把外域对象伪装成自己的
      object: { id: foreignId, attributedTo: ALICE, content: 'forged' },
    };
    const fetcher = countingFetcher({
      [foreignId]: { id: foreignId, attributedTo: ALICE, content: 'authoritative' },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, true);
    assert.deepEqual(fetcher.calls, [foreignId]);
    assert.equal(fetcher.calls.length, 1);
    const resolved = result.activity.object as InboxActivity;
    assert.equal(resolved.content, 'authoritative');
    assert.equal(resolved.attributedTo, ALICE);
    assert.equal(result.fetchedObjectId, foreignId);
  });

  it('取回的权威副本 attributedTo 与 actor 不一致时忽略活动', async () => {
    const foreignId = 'https://bob.example/notes/9';
    const activity: InboxActivity = {
      type: 'Announce',
      actor: ALICE,
      object: { id: foreignId, attributedTo: ALICE },
    };
    const fetcher = countingFetcher({
      [foreignId]: { id: foreignId, attributedTo: BOB },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'attribution_mismatch');
    assert.deepEqual(fetcher.calls, [foreignId]);
  });

  it('Announce 裸 id 跨域对象同样取回并核对归属', async () => {
    const foreignId = 'https://cdn.example/articles/3';
    const activity: InboxActivity = {
      type: 'Announce',
      actor: ALICE,
      object: foreignId,
    };
    const fetcher = countingFetcher({
      [foreignId]: { id: foreignId, attributedTo: ALICE },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, true);
    assert.equal(fetcher.calls.length, 1);
  });

  it('同域内嵌对象 attributedTo 不符仍被拒绝', async () => {
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: 'https://alice.example/notes/2', attributedTo: BOB },
    };

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: countingFetcher(),
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'attribution_mismatch');
  });
});

describe('对象 id 与取回安全', () => {
  it('拒绝 http 对象 id', async () => {
    const activity: InboxActivity = {
      type: 'Announce',
      actor: ALICE,
      object: 'http://bob.example/notes/9',
    };
    const fetcher = countingFetcher();

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'invalid_object_id');
    assert.deepEqual(fetcher.calls, []);
  });

  it('拒绝携带 userinfo 的对象 id', async () => {
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: {
        id: 'https://alice@bob.example/notes/9',
        attributedTo: ALICE,
      },
    };

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: countingFetcher(),
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'invalid_object_id');
  });

  it('拒绝跟随到其他域名的重定向，且不再次发起请求', async () => {
    const foreignId = 'https://bob.example/notes/9';
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: foreignId, attributedTo: ALICE },
    };
    const fetcher = countingFetcher({
      [foreignId]: {
        status: 302,
        location: 'https://evil.example/notes/9',
      },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'cross_origin_redirect');
    assert.deepEqual(fetcher.calls, [foreignId]);
    assert.equal(fetcher.calls.length, 1);
  });

  it('同域重定向也不跟随（每次授权只取回一次）', async () => {
    const foreignId = 'https://bob.example/notes/9';
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: foreignId, attributedTo: ALICE },
    };
    const fetcher = countingFetcher({
      [foreignId]: {
        status: 301,
        location: 'https://bob.example/notes/9-moved',
      },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'fetch_failed');
    assert.equal(fetcher.calls.length, 1);
  });

  it('取回器抛错时映射为 fetch_failed 且不重试', async () => {
    const foreignId = 'https://bob.example/notes/9';
    const activity: InboxActivity = {
      type: 'Announce',
      actor: ALICE,
      object: foreignId,
    };
    const fetcher = countingFetcher({
      [foreignId]: Promise.reject(new Error('connection reset')),
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'fetch_failed');
    assert.equal(fetcher.calls.length, 1);
  });

  it('取回内容缺少 attributedTo 时按归属不符忽略', async () => {
    const foreignId = 'https://bob.example/notes/9';
    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: foreignId, attributedTo: ALICE },
    };
    const fetcher = countingFetcher({
      [foreignId]: { id: foreignId },
    });

    const result = await authorizeInboxActivity(activity, {
      signature: signature(ALICE_KEY),
      fetchObject: fetcher,
    });

    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'attribution_mismatch');
  });
});
