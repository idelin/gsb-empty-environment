import { describe, it, mock } from 'node:test';
import assert from 'node:assert/strict';
import {
  KeyFetchError,
  MAX_KEY_RESPONSE_BYTES,
  fetchKeyMaterial,
  parseJwkSet,
} from '../index.ts';
import type { FetchedKeyMaterial } from '../index.ts';

const targetUrl = 'https://keys.example.com/v1/keys/tenant-a.jwk';

function jsonResponse(body: string, init?: ResponseInit): Response {
  return new Response(body, {
    status: 200,
    headers: { 'content-type': 'application/json' },
    ...init,
  });
}

describe('fetchKeyMaterial - 重定向与协议', () => {
  it('收到 3xx 时拒绝且不跟随重定向', async () => {
    const fetchImpl = mock.fn(async () =>
      new Response(null, {
        status: 302,
        headers: { location: 'https://evil.example.org/attacker.jwk' },
      }),
    );
    await assert.rejects(
      fetchKeyMaterial(targetUrl, { fetch: fetchImpl }),
      (error: unknown) => {
        assert.ok(error instanceof KeyFetchError);
        assert.equal(error.reason, 'redirect_blocked');
        return true;
      },
    );
    assert.equal(fetchImpl.mock.callCount(), 1);
    const passedInit = fetchImpl.mock.calls[0]!.arguments[1] as RequestInit;
    assert.equal(passedInit.redirect, 'manual');
  });

  it('opaqueredirect 响应同样拒绝', async () => {
    const fetchImpl = mock.fn(async () =>
      (() => {
        const opaque = new Response('', { status: 200 });
        Object.defineProperty(opaque, 'type', { value: 'opaqueredirect', configurable: true });
        return opaque;
      })(),
    );
    await assert.rejects(
      fetchKeyMaterial(targetUrl, { fetch: fetchImpl }),
      (error: unknown) => error instanceof KeyFetchError && error.reason === 'redirect_blocked',
    );
  });

  it('非 https URL 在发请求前直接拒绝', async () => {
    const fetchImpl = mock.fn(async () => jsonResponse('{}'));
    await assert.rejects(
      fetchKeyMaterial('http://keys.example.com/v1/keys/tenant-a.jwk', { fetch: fetchImpl }),
      (error: unknown) => {
        assert.ok(error instanceof KeyFetchError);
        assert.equal(error.reason, 'not_https');
        return true;
      },
    );
    assert.equal(fetchImpl.mock.callCount(), 0);
  });

  it('请求不携带凭据', async () => {
    const fetchImpl = mock.fn(async () => jsonResponse('{"keys":[]}'));
    await fetchKeyMaterial(targetUrl, { fetch: fetchImpl });
    const passedInit = fetchImpl.mock.calls[0]!.arguments[1] as RequestInit;
    assert.equal(passedInit.credentials, 'omit');
  });

  it('非 2xx 响应抛出 http_error', async () => {
    const fetchImpl = mock.fn(async () => new Response(null, { status: 403 }));
    await assert.rejects(
      fetchKeyMaterial(targetUrl, { fetch: fetchImpl }),
      (error: unknown) => error instanceof KeyFetchError && error.reason === 'http_error',
    );
  });
});

describe('fetchKeyMaterial - 64 KiB 上限', () => {
  it('content-length 超过 64 KiB 时拒绝', async () => {
    const fetchImpl = mock.fn(async () =>
      new Response(new Uint8Array(8), {
        status: 200,
        headers: { 'content-length': String(MAX_KEY_RESPONSE_BYTES + 1) },
      }),
    );
    await assert.rejects(
      fetchKeyMaterial(targetUrl, { fetch: fetchImpl }),
      (error: unknown) => error instanceof KeyFetchError && error.reason === 'response_too_large',
    );
  });

  it('分块流式响应累计超过 64 KiB 时拒绝并取消读取', async () => {
    const limit = MAX_KEY_RESPONSE_BYTES;
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('a'.repeat(limit - 10)));
        controller.enqueue(encoder.encode('b'.repeat(64)));
        controller.close();
      },
    });
    const fetchImpl = mock.fn(async () =>
      new Response(stream, { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    await assert.rejects(
      fetchKeyMaterial(targetUrl, { fetch: fetchImpl }),
      (error: unknown) => error instanceof KeyFetchError && error.reason === 'response_too_large',
    );
  });

  it('恰好 64 KiB 的响应允许通过', async () => {
    const body = new Uint8Array(MAX_KEY_RESPONSE_BYTES);
    const fetchImpl = mock.fn(async () =>
      new Response(body, { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    const material = await fetchKeyMaterial(targetUrl, { fetch: fetchImpl });
    assert.equal(material.bytes.byteLength, MAX_KEY_RESPONSE_BYTES);
  });
});

describe('parseJwkSet', () => {
  function materialFor(json: string): FetchedKeyMaterial {
    return {
      url: targetUrl,
      contentType: 'application/json',
      bytes: new TextEncoder().encode(json),
    };
  }

  it('按 kid 选取 JWK Set 中的密钥', () => {
    const material = materialFor(JSON.stringify({
      keys: [
        { kid: 'other', kty: 'EC', crv: 'P-256', x: 'x1', y: 'y1' },
        { kid: 'tenant-a', kty: 'EC', crv: 'P-256', x: 'x2', y: 'y2' },
      ],
    }));
    const key = parseJwkSet(material, 'tenant-a');
    assert.equal(key?.x, 'x2');
    assert.equal(parseJwkSet(material, 'missing'), null);
  });

  it('非法 JSON 或缺少 keys 数组时报错', () => {
    assert.throws(() => parseJwkSet(materialFor('not-json'), 'tenant-a'), KeyFetchError);
    assert.throws(() => parseJwkSet(materialFor('{"foo":1}'), 'tenant-a'), KeyFetchError);
  });
});
