import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  buildPreflightCacheKey,
  InvalidIpError,
  PrivateNetworkAccessGuard,
  PrivateNetworkAccessDeniedError,
  RebindingDetectedError,
  type GuardRequest,
  type PreflightResponse,
  type PreflightTransport,
} from '../index.ts';

interface SentPreflight {
  request: GuardRequest;
  headers: Record<string, string>;
}

class FakeTransport implements PreflightTransport {
  readonly sent: SentPreflight[] = [];
  response: PreflightResponse = {
    status: 204,
    headers: { 'Access-Control-Allow-Private-Network': 'true' },
  };

  async sendPreflight(request: GuardRequest, headers: Record<string, string>) {
    this.sent.push({ request, headers: { ...headers } });
    return this.response;
  }
}

function makeGuard(now: { current: number }, response?: PreflightResponse) {
  const transport = new FakeTransport();
  if (response !== undefined) {
    transport.response = response;
  }
  const guard = new PrivateNetworkAccessGuard({
    transport,
    now: () => now.current,
  });
  return { guard, transport };
}

function header(headers: Record<string, string>, name: string): string | undefined {
  const lower = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === lower) {
      return value;
    }
  }
  return undefined;
}

const privateRequest: GuardRequest = {
  url: 'http://router.local/admin',
  method: 'POST',
  targetIp: '192.168.1.1',
  initiator: 'public',
};

describe('preflight decision', () => {
  it('skips preflight when initiator is not more public than target', async () => {
    const now = { current: 1000 };
    const { guard, transport } = makeGuard(now);

    const sameSpace = await guard.authorize({ ...privateRequest, initiator: 'private' });
    assert.equal(sameSpace.preflightRequired, false);

    const lessPublic = await guard.authorize({
      ...privateRequest,
      targetIp: '8.8.8.8',
      initiator: 'loopback',
    });
    assert.equal(lessPublic.preflightRequired, false);
    assert.equal(transport.sent.length, 0);
  });

  it('sends an OPTIONS preflight with the private-network opt-in headers', async () => {
    const now = { current: 1000 };
    const { guard, transport } = makeGuard(now);

    const decision = await guard.authorize(privateRequest);
    assert.equal(transport.sent.length, 1);
    assert.equal(header(transport.sent[0].headers, 'Access-Control-Request-Method'), 'POST');
    assert.equal(
      header(transport.sent[0].headers, 'Access-Control-Request-Private-Network'),
      'true',
    );
    assert.deepEqual(decision, {
      preflightRequired: true,
      cached: false,
      targetIp: '192.168.1.1',
      expiresAt: 11_000,
    });
  });

  it('accepts an initiator given as a peer IP literal', async () => {
    const now = { current: 0 };
    const { guard, transport } = makeGuard(now);
    await guard.authorize({ ...privateRequest, initiator: '203.0.113.7' });
    assert.equal(transport.sent.length, 1);
  });

  it('rejects non-2xx responses', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now, {
      status: 403,
      headers: { 'Access-Control-Allow-Private-Network': 'true' },
    });
    await assert.rejects(() => guard.authorize(privateRequest), (error: unknown) => {
      assert.ok(error instanceof PrivateNetworkAccessDeniedError);
      assert.equal(error.reason, 'bad-status');
      assert.equal(error.status, 403);
      return true;
    });
  });

  it('rejects missing or wrong Access-Control-Allow-Private-Network', async () => {
    const now = { current: 0 };
    const missing = makeGuard(now, { status: 200, headers: {} });
    await assert.rejects(
      () => missing.guard.authorize(privateRequest),
      /missing-allow-private-network/,
    );

    const wrong = makeGuard(now, {
      status: 200,
      headers: { 'Access-Control-Allow-Private-Network': 'false' },
    });
    await assert.rejects(
      () => wrong.guard.authorize(privateRequest),
      /invalid-allow-private-network/,
    );
  });

  it('matches the header case-insensitively and tolerates whitespace', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now, {
      status: 200,
      headers: { 'ACCESS-CONTROL-ALLOW-PRIVATE-NETWORK': ' true ' },
    });
    const decision = await guard.authorize(privateRequest);
    assert.equal(decision.preflightRequired, true);
  });
});

describe('preflight cache key', () => {
  it('includes the normalized target IP, origin and method', () => {
    assert.equal(
      buildPreflightCacheKey('http://router.local', 'post', '192.168.1.1'),
      'http://router.local\nPOST\n192.168.1.1',
    );
  });

  it('collapses mapped IPv6 onto the embedded IPv4 key', () => {
    const fromMapped = buildPreflightCacheKey(
      'http://router.local',
      'GET',
      '::ffff:192.168.1.1',
    );
    const fromV4 = buildPreflightCacheKey('http://router.local', 'GET', '192.168.1.1');
    assert.equal(fromMapped, fromV4);
  });

  it('reuses the cached entry within TTL and refetches after expiry', async () => {
    const now = { current: 0 };
    const { guard, transport } = makeGuard(now);

    const first = await guard.authorize(privateRequest);
    assert.equal(first.cached, false);
    const second = await guard.authorize(privateRequest);
    assert.equal(second.cached, true);
    assert.equal(transport.sent.length, 1);

    now.current = 10_001;
    const third = await guard.authorize(privateRequest);
    assert.equal(third.cached, false);
    assert.equal(transport.sent.length, 2);
  });

  it('honors Access-Control-Max-Age with an upper bound', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now, {
      status: 204,
      headers: {
        'Access-Control-Allow-Private-Network': 'true',
        'Access-Control-Max-Age': '60',
      },
    });
    const decision = await guard.authorize(privateRequest);
    assert.equal(decision.preflightRequired, true);
    if (decision.preflightRequired) {
      assert.equal(decision.expiresAt, 60_000);
    }
  });

  it('keeps distinct IPs, origins and methods in separate cache entries', async () => {
    const now = { current: 0 };
    const { guard, transport } = makeGuard(now);

    await guard.authorize(privateRequest);
    await guard.authorize({ ...privateRequest, targetIp: '192.168.1.2' });
    await guard.authorize({ ...privateRequest, url: 'http://other.local/x' });
    await guard.authorize({ ...privateRequest, method: 'GET' });
    assert.equal(transport.sent.length, 4);

    await guard.authorize(privateRequest);
    assert.equal(transport.sent.length, 4);
  });
});

describe('DNS rebinding defense', () => {
  it('does not reuse a preflight when the same origin resolves to a new IP', async () => {
    const now = { current: 0 };
    const { guard, transport } = makeGuard(now);

    await guard.authorize({ ...privateRequest, targetIp: '192.168.1.1' });
    const rebound = await guard.authorize({ ...privateRequest, targetIp: '10.0.0.9' });

    assert.equal(transport.sent.length, 2);
    assert.equal(rebound.preflightRequired, true);
    if (rebound.preflightRequired) {
      assert.equal(rebound.targetIp, '10.0.0.9');
    }
  });

  it('aborts when the actual connection IP differs from the preflight IP', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now);
    const authorization = await guard.authorize(privateRequest);

    assert.throws(
      () => guard.verifyConnection(authorization, '10.0.0.9'),
      (error: unknown) => {
        assert.ok(error instanceof RebindingDetectedError);
        assert.equal(error.preflightIp, '192.168.1.1');
        assert.equal(error.actualIp, '10.0.0.9');
        return true;
      },
    );
  });

  it('accepts the connection when the peer IP matches, including via mapped form', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now);
    const authorization = await guard.authorize(privateRequest);

    assert.doesNotThrow(() => guard.verifyConnection(authorization, '192.168.1.1'));
    assert.doesNotThrow(() =>
      guard.verifyConnection(authorization, '::ffff:192.168.1.1'),
    );
  });

  it('skips verification for requests that never needed a preflight', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now);
    const authorization = await guard.authorize({
      ...privateRequest,
      initiator: 'private',
    });
    assert.doesNotThrow(() => guard.verifyConnection(authorization, '8.8.8.8'));
  });
});

describe('mapped address end-to-end', () => {
  it('classifies, authorizes and caches a ::ffff private target identically to IPv4', async () => {
    const now = { current: 0 };
    const { guard, transport } = makeGuard(now);

    const mapped = await guard.authorize({
      ...privateRequest,
      targetIp: '::ffff:192.168.1.1',
    });
    const v4 = await guard.authorize({ ...privateRequest, targetIp: '192.168.1.1' });

    assert.equal(transport.sent.length, 1);
    assert.equal(mapped.preflightRequired, true);
    assert.equal(v4.preflightRequired, true);
    if (mapped.preflightRequired && v4.preflightRequired) {
      assert.equal(mapped.targetIp, v4.targetIp);
      assert.equal(mapped.targetIp, '192.168.1.1');
    }
  });

  it('propagates an invalid peer IP as InvalidIpError', async () => {
    const now = { current: 0 };
    const { guard } = makeGuard(now);
    await assert.rejects(
      () => guard.authorize({ ...privateRequest, targetIp: 'not-an-ip' }),
      InvalidIpError,
    );
  });
});
