import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { createAdjudicator } from '../index.ts';

const POLICY = {
  fenceOrigin: 'https://fence.example',
  allowedOrigins: ['https://api.example:8443', 'wss://push.example'],
};

describe('outer URL reads', () => {
  it('always denies reading the embedder address', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateOuterUrlRead('window.top.location.href');
    assert.equal(decision.allowed, false);
    assert.equal(decision.reason, 'outer-url-disclosed');
  });

  it('denies via the generic dispatch entry point as well', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicate({
      kind: 'outer-url-read',
      property: 'document.referrer',
    });
    assert.equal(decision.allowed, false);
  });
});

describe('cross-context messages', () => {
  it('denies messages targeting window.top', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicate({
      kind: 'cross-context-message',
      targetContext: 'top',
      targetUrl: 'https://embedder.example',
    });
    assert.equal(decision.allowed, false);
    assert.equal(decision.reason, 'top-communication-disabled');
  });

  it('denies parent and frameElement targets', () => {
    const judge = createAdjudicator(POLICY);
    for (const targetContext of ['parent', 'frameElement']) {
      assert.equal(
        judge.adjudicate({ kind: 'cross-context-message', targetContext }).allowed,
        false,
      );
    }
  });

  it('keeps self-targeted messages working', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicate({
      kind: 'cross-context-message',
      targetContext: 'self',
    });
    assert.equal(decision.allowed, true);
    assert.equal(decision.reason, 'self-message-allowed');
  });
});

describe('network requests', () => {
  it('allows same-origin absolute requests', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest('https://fence.example/assets/app.js');
    assert.equal(decision.allowed, true);
    assert.equal(decision.reason, 'same-origin-allowed');
    assert.equal(decision.targetOrigin, 'https://fence.example');
  });

  it('preserves same-origin relative requests via the document base', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest('/api/state');
    assert.equal(decision.allowed, true);
    assert.equal(decision.resolvedUrl, 'https://fence.example/api/state');
    assert.equal(decision.reason, 'same-origin-allowed');
  });

  it('treats blob URLs carrying the fence origin as same-origin', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest('blob:https://fence.example/uuid');
    assert.equal(decision.allowed, true);
    assert.equal(decision.reason, 'same-origin-allowed');
  });

  it('allows origins explicitly listed by the policy', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest('https://api.example:8443/v1/data');
    assert.equal(decision.allowed, true);
    assert.equal(decision.reason, 'allowlisted-origin');
  });

  it('matches scheme/host/port exactly, including websocket schemes', () => {
    const judge = createAdjudicator(POLICY);
    assert.equal(
      judge.adjudicateNetworkRequest('wss://push.example/feed').allowed,
      true,
    );
    assert.equal(
      judge.adjudicateNetworkRequest('https://push.example/feed').allowed,
      false,
    );
    assert.equal(
      judge.adjudicateNetworkRequest('https://api.example/v1/data').allowed,
      false,
    );
  });

  it('denies destinations absent from the allow-list', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest('https://tracker.example/collect');
    assert.equal(decision.allowed, false);
    assert.equal(decision.reason, 'origin-not-allowlisted');
    assert.equal(decision.targetOrigin, 'https://tracker.example');
  });

  it('denies opaque-origin data and javascript URLs', () => {
    const judge = createAdjudicator(POLICY);
    for (const url of ['data:text/plain,leak', 'javascript:alert(1)']) {
      const decision = judge.adjudicateNetworkRequest(url);
      assert.equal(decision.allowed, false);
      assert.equal(decision.reason, 'opaque-origin');
    }
  });

  it('denies unparseable URLs instead of throwing', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest('http://');
    assert.equal(decision.allowed, false);
    assert.equal(decision.reason, 'unparseable-url');
  });

  it('ignores userinfo when deriving the destination origin', () => {
    const judge = createAdjudicator(POLICY);
    const decision = judge.adjudicateNetworkRequest(
      'https://fence.example@evil.example/steal',
    );
    assert.equal(decision.allowed, false);
    assert.equal(decision.targetOrigin, 'https://evil.example');
  });
});

describe('policy compilation', () => {
  it('rejects opaque fence origins and allow-list entries', () => {
    assert.throws(
      () => createAdjudicator({ fenceOrigin: 'data:text/plain,x' }),
      /concrete origin/,
    );
    assert.throws(
      () =>
        createAdjudicator({
          fenceOrigin: 'https://fence.example',
          allowedOrigins: ['data:text/plain,x'],
        }),
      /Opaque origins/,
    );
  });

  it('rejects a document base on another origin', () => {
    assert.throws(
      () =>
        createAdjudicator({
          fenceOrigin: 'https://fence.example',
          documentBaseUrl: 'https://other.example/app/',
        }),
      /must match fence origin/,
    );
  });
});
