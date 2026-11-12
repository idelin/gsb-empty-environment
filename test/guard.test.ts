import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHarness } from './harness.ts';
import {
  ClipboardAbortError,
  ExpiredActivationError,
  NoTransientActivationError,
  PermissionDeniedError,
  StaleDocumentGenerationError,
  StaleFocusGenerationError,
  TabNotFoundError,
} from '../src/errors.ts';

test('全部绑定复核通过时才执行一次剪贴板写入，并消费单次授权', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const done = h.guard.copy('a', 'hello');
  await h.flushMicrotasks();
  assert.equal(h.permissioner.pendingCount(), 1);

  const grant = h.permissioner.grantNext();
  await done;

  assert.equal(h.clipboard.totalCalls(), 1);
  assert.equal(h.clipboard.readContents(), 'hello');
  assert.equal(h.permissioner.isConsumed(grant.grantId), true);
  assert.deepEqual(h.permissioner.consumedGrantIds, [grant.grantId]);
  assert.equal(h.clipboard.calls[0].context.grantId, grant.grantId);
});

test('多个标签页：A 的焦点代失配请求被拒绝，不触碰 B 的授权', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.environment.openTab('b');

  h.activations.signalUserGesture('a');
  const copyA = h.guard.copy('a', 'from-a');
  await h.flushMicrotasks();
  const requestA = h.permissioner.pendingRequestIds()[0];

  h.activations.signalUserGesture('b');
  h.environment.focusTab('b');
  const copyB = h.guard.copy('b', 'from-b');
  await h.flushMicrotasks();

  const grantB = h.permissioner.grantRequest(
    h.permissioner.pendingRequestIds().find((id) => id !== requestA)!,
  );
  await copyB;

  const grantA = h.permissioner.grantRequest(requestA);
  await assert.rejects(copyA, StaleFocusGenerationError);

  assert.deepEqual(
    h.clipboard.callContexts().map((context) => context.tabId),
    ['b'],
  );
  assert.equal(h.permissioner.isConsumed(grantB.grantId), true);
  assert.equal(h.permissioner.isConsumed(grantA.grantId), true);
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [grantA.grantId],
  );
});

test('许可回调到达前页面导航：文档代失配，授权作废且零剪贴板调用', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const copy = h.guard.copy('a', 'stale');
  await h.flushMicrotasks();
  const requestId = h.permissioner.pendingRequestIds()[0];

  h.environment.navigate('a');
  const grant = h.permissioner.grantRequest(requestId);

  await assert.rejects(copy, StaleDocumentGenerationError);
  assert.equal(h.clipboard.totalCalls(), 0);
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [grant.grantId],
  );
});

test('等待期间标签页关闭：回调复核报 TabNotFoundError 且零越权调用', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const copy = h.guard.copy('a', 'orphan');
  await h.flushMicrotasks();
  const requestId = h.permissioner.pendingRequestIds()[0];

  h.environment.closeTab('a');
  const grant = h.permissioner.grantRequest(requestId);

  await assert.rejects(copy, TabNotFoundError);
  assert.equal(h.clipboard.totalCalls(), 0);
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [grant.grantId],
  );
});

test('过期激活：手势 TTL 到期后的回调被拒绝，另一请求不受影响', async () => {
  const h = createHarness({ activationTtlMs: 50 });
  h.environment.openTab('a', { focused: true });

  h.activations.signalUserGesture('a');
  const slowCopy = h.guard.copy('a', 'expired');
  await h.flushMicrotasks();
  const slowRequest = h.permissioner.pendingRequestIds()[0];

  h.clock.advance(20);
  h.activations.signalUserGesture('a');
  const fastCopy = h.guard.copy('a', 'fresh');
  await h.flushMicrotasks();

  const fastRequest = h.permissioner
    .pendingRequestIds()
    .find((id) => id !== slowRequest)!;
  const freshGrant = h.permissioner.grantRequest(fastRequest);
  await fastCopy;

  h.clock.advance(60);

  const staleGrant = h.permissioner.grantRequest(slowRequest);
  await assert.rejects(slowCopy, ExpiredActivationError);

  assert.equal(h.clipboard.totalCalls(), 1);
  assert.equal(h.clipboard.calls[0].text, 'fresh');
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [staleGrant.grantId],
  );
  assert.equal(h.permissioner.isConsumed(freshGrant.grantId), true);
});

test('无激活手势直接发起动作：不请求许可、不接触剪贴板', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });

  await assert.rejects(h.guard.copy('a', 'blocked'), NoTransientActivationError);

  assert.equal(h.permissioner.pendingCount(), 0);
  assert.equal(h.permissioner.issuedGrants.length, 0);
  assert.equal(h.clipboard.totalCalls(), 0);
});

test('复制动作取消：等待中中止不触发回调，事后到达的授权被作废', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const controller = new AbortController();
  const copy = h.guard.copy('a', 'cancel-me', controller.signal);
  await h.flushMicrotasks();
  const requestId = h.permissioner.pendingRequestIds()[0];

  controller.abort();
  assert.equal(h.permissioner.pendingCount(), 1);

  const grant = h.permissioner.grantRequest(requestId);
  await assert.rejects(copy, ClipboardAbortError);
  assert.equal(h.clipboard.totalCalls(), 0);
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [grant.grantId],
  );
  assert.equal(h.permissioner.isConsumed(grant.grantId), true);
});

test('授权已到达后再中止：授权作废，剪贴板零调用', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const controller = new AbortController();
  const copy = h.guard.copy('a', 'late-abort', controller.signal);
  await h.flushMicrotasks();
  const requestId = h.permissioner.pendingRequestIds()[0];

  let releaseGate: () => void = () => {};
  h.permissioner.resolutionGate = () =>
    new Promise<void>((resolve) => {
      releaseGate = resolve;
    });
  const grant = h.permissioner.grantRequest(requestId);
  await h.flushMicrotasks();

  controller.abort();
  releaseGate();

  await assert.rejects(copy, ClipboardAbortError);
  assert.equal(h.clipboard.totalCalls(), 0);
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [grant.grantId],
  );
});

test('许可被宿主拒绝：抛 PermissionDeniedError，且不调用剪贴板', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const copy = h.guard.copy('a', 'denied');
  await h.flushMicrotasks();
  h.permissioner.denyNext();

  await assert.rejects(copy, PermissionDeniedError);
  assert.equal(h.clipboard.totalCalls(), 0);
  assert.equal(h.permissioner.issuedGrants.length, 0);
});

test('paste 成功路径携带读取授权上下文', async () => {
  const h = createHarness({ initialClipboard: 'pasted' });
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');

  const paste = h.guard.paste('a');
  await h.flushMicrotasks();
  const grant = h.permissioner.grantNext();
  const text = await paste;

  assert.equal(text, 'pasted');
  assert.deepEqual(
    h.clipboard.callsFor('read').map((call) => call.context.grantId),
    [grant.grantId],
  );
});

test('系统剪贴板底层抛错：授权作废且错误向上传播', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.activations.signalUserGesture('a');
  h.clipboard.failNextWith = 'simulated OS clipboard failure';

  const copy = h.guard.copy('a', 'boom');
  await h.flushMicrotasks();
  const grant = h.permissioner.grantNext();

  await assert.rejects(copy, /simulated OS clipboard failure/);
  assert.equal(h.permissioner.isConsumed(grant.grantId), true);
  assert.deepEqual(h.permissioner.consumedGrantIds, [grant.grantId]);
  assert.deepEqual(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
    [grant.grantId],
  );
  assert.equal(h.clipboard.totalCalls(), 0);
});

test('失败请求不消费另一请求的许可（混合拒绝/失配综合场景）', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.environment.openTab('b');

  h.activations.signalUserGesture('a');
  const copyA = h.guard.copy('a', 'a-text');
  await h.flushMicrotasks();
  const requestA = h.permissioner.pendingRequestIds()[0];

  h.activations.signalUserGesture('b');
  h.environment.focusTab('b');
  const copyB = h.guard.copy('b', 'b-text');
  await h.flushMicrotasks();
  const requestB = h.permissioner
    .pendingRequestIds()
    .find((id) => id !== requestA)!;

  h.permissioner.denyRequest(requestA);
  await assert.rejects(copyA, PermissionDeniedError);

  const grantB = h.permissioner.grantRequest(requestB);
  await copyB;

  assert.equal(h.permissioner.isConsumed(grantB.grantId), true);
  assert.deepEqual(
    h.clipboard.callContexts().map((context) => context.requestId),
    [requestB],
  );
  assert.deepEqual(h.permissioner.invalidatedGrants, []);
});

test('系统剪贴板零越权：每条调用都能对上当次已消费授权，无无授权调用', async () => {
  const h = createHarness();
  h.environment.openTab('a', { focused: true });
  h.environment.openTab('b');

  const failures: Promise<unknown>[] = [];

  h.activations.signalUserGesture('a');
  failures.push(
    h.guard
      .copy('a', 'will-navigate')
      .then(
        () => assert.fail('should have failed'),
        (error) => assert.ok(error instanceof StaleDocumentGenerationError),
      ),
  );
  await h.flushMicrotasks();
  const navRequest = h.permissioner.pendingRequestIds()[0];
  h.environment.navigate('a');

  h.activations.signalUserGesture('b');
  h.environment.focusTab('b');
  const goodCopy = h.guard.copy('b', 'good');
  await h.flushMicrotasks();
  const goodRequest = h.permissioner
    .pendingRequestIds()
    .find((id) => id !== navRequest)!;

  h.permissioner.grantRequest(goodRequest);
  await goodCopy;
  h.permissioner.grantRequest(navRequest);
  await Promise.all(failures);

  assert.equal(h.clipboard.totalCalls(), 1);
  for (const call of h.clipboard.calls) {
    const grant = h.permissioner.issuedGrants.find(
      (item) => item.grantId === call.context.grantId,
    );
    assert.ok(grant, 'clipboard call must reference a real grant');
    assert.equal(grant!.mode, call.mode);
    assert.equal(grant!.snapshot.tabId, call.context.tabId);
    assert.equal(h.permissioner.isConsumed(grant!.grantId), true);
  }

  const consumed = new Set(h.permissioner.consumedGrantIds);
  const invalidated = new Set(
    h.permissioner.invalidatedGrants.map((item) => item.grantId),
  );
  for (const grant of h.permissioner.issuedGrants) {
    assert.ok(
      consumed.has(grant.grantId) !== invalidated.has(grant.grantId),
      `grant ${grant.grantId} must be either consumed or invalidated, never both`,
    );
  }
});
