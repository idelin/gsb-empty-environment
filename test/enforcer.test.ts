import assert from 'node:assert/strict';
import { afterEach, describe, it } from 'node:test';

import {
  FenceViolationError,
  installFence,
  type AncestorOriginsLike,
  type FenceWindow,
} from '../index.ts';

const POLICY = {
  fenceOrigin: 'https://fence.example',
  documentBaseUrl: 'https://fence.example/app/index.html',
  allowedOrigins: ['https://api.example', 'wss://api.example'],
};

function createMockWindow(): FenceWindow {
  const calls: string[] = [];
  const ancestorOrigins: AncestorOriginsLike = Object.assign(
    ['https://embedder.example'],
    {
      length: 1,
      item: (index: number) => (index === 0 ? 'https://embedder.example' : null),
      contains: (origin: string) => origin === 'https://embedder.example',
    },
  );

  class MockXHR {
    openedWith: string | null = null;
    open(method: string, url: string): void {
      this.openedWith = `${method} ${url}`;
    }
  }

  const win: FenceWindow = {
    calls,
    location: { href: 'https://fence.example/app/index.html', origin: 'https://fence.example' },
    document: { referrer: 'https://embedder.example/outer', ancestorOrigins },
    postMessage(message: unknown) {
      calls.push(`postMessage:${String(message)}`);
    },
    async fetch(input: string | { url?: string; href?: string }) {
      calls.push(`fetch:${typeof input === 'string' ? input : input.url ?? input.href}`);
      return { ok: true };
    },
    navigator: {
      sendBeacon(url: string) {
        calls.push(`beacon:${url}`);
        return true;
      },
    },
    XMLHttpRequest: MockXHR,
    WebSocket: class MockSocket {
      url: string;
      constructor(url: string) {
        this.url = url;
        calls.push(`websocket:${url}`);
      }
    },
    EventSource: class MockEventSource {
      url: string;
      constructor(url: string) {
        this.url = url;
        calls.push(`eventsource:${url}`);
      }
    },
  };
  return win;
}

let enforcement: ReturnType<typeof installFence> | undefined;

afterEach(() => {
  enforcement?.uninstall();
  enforcement = undefined;
});

describe('outer address isolation', () => {
  it('never leaks the outer URL through top/parent navigation', () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);

    assert.equal(win.top?.location.href, win.location.href);
    assert.equal(win.parent?.location.origin, 'https://fence.example');
    assert.equal(win.frameElement, null);
    assert.equal(win.document.referrer, '');
    assert.equal(win.document.ancestorOrigins?.length, 0);
    assert.equal(win.document.ancestorOrigins?.contains('https://embedder.example'), false);
  });

  it('throws FenceViolationError on direct outer-read adjudication paths', () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);

    assert.throws(
      () => win.top?.postMessage('payload', '*'),
      (error: unknown) =>
        error instanceof FenceViolationError &&
        error.decision.reason === 'top-communication-disabled',
    );
  });

  it('blocks window.postMessage calls aimed at the embedder', () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);

    assert.throws(() => win.postMessage('hello', 'https://embedder.example'));
    assert.deepEqual(win.calls, []);
  });

  it('reports every violation through onViolation', () => {
    const win = createMockWindow();
    const violations: string[] = [];
    enforcement = installFence(win, POLICY, {
      onViolation: (decision) => violations.push(decision.reason),
    });

    assert.throws(() => win.postMessage('x'));
    assert.throws(() => win.fetch?.('https://tracker.example/ping'));
    assert.deepEqual(violations, [
      'top-communication-disabled',
      'origin-not-allowlisted',
    ]);
  });
});

describe('network channel enforcement', () => {
  it('allows same-origin and policy-listed fetch calls', async () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);

    await win.fetch?.('/same-origin/data');
    await win.fetch?.('https://api.example/v1');
    assert.deepEqual(win.calls, [
      'fetch:/same-origin/data',
      'fetch:https://api.example/v1',
    ]);
  });

  it('blocks non-allowlisted fetch calls before they leave the fence', async () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);

    await assert.rejects(
      async () => win.fetch?.('https://tracker.example/collect'),
      (error: unknown) =>
        error instanceof FenceViolationError &&
        error.decision.reason === 'origin-not-allowlisted',
    );
    assert.deepEqual(win.calls, []);
  });

  it('guards XMLHttpRequest, sendBeacon, WebSocket and EventSource', () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);

    const xhr = new win.XMLHttpRequest!();
    xhr.open('GET', '/feed.xml');
    assert.equal(xhr.openedWith, 'GET /feed.xml');

    assert.equal(win.navigator?.sendBeacon?.('/telemetry'), true);
    assert.doesNotThrow(() => new win.WebSocket!('wss://api.example/socket'));
    assert.doesNotThrow(() => new win.EventSource!('/events'));

    assert.throws(() => new win.WebSocket!('wss://evil.example/s'));
    assert.throws(() => new win.EventSource!('https://evil.example/s'));
    assert.throws(() => xhr.open('GET', 'https://evil.example/x'));
    assert.throws(() => win.navigator?.sendBeacon?.('https://evil.example/b'));

    assert.deepEqual(win.calls, [
      'beacon:/telemetry',
      'websocket:wss://api.example/socket',
      'eventsource:/events',
    ]);
  });
});

describe('uninstall', () => {
  it('restores original globals and permits reinstallation', () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);
    enforcement.uninstall();

    assert.equal(win.document.referrer, 'https://embedder.example/outer');
    assert.equal(win.document.ancestorOrigins?.length, 1);
    assert.doesNotThrow(() => win.postMessage('restored'));
    assert.deepEqual(win.calls, ['postMessage:restored']);

    enforcement = installFence(win, POLICY);
    assert.throws(() => win.postMessage('blocked again'));
  });

  it('refuses nested installation on the same window', () => {
    const win = createMockWindow();
    enforcement = installFence(win, POLICY);
    assert.throws(() => installFence(win, POLICY), /already installed/);
  });
});
