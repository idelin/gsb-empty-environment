import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  DEFAULT_ALLOWED_CRIT,
  JwsHeaderError,
  admitCompactJws,
  admitProtectedHeader,
  base64urlToBytes,
  bytesToBase64url,
} from './jws-header.ts';

const encoder = new TextEncoder();

function headerSegment(header: Record<string, unknown>): string {
  return bytesToBase64url(encoder.encode(JSON.stringify(header)));
}

function expectCode(action: () => unknown, code: string): void {
  assert.throws(action, (error: unknown) => {
    assert.ok(error instanceof JwsHeaderError);
    assert.equal((error as JwsHeaderError).code, code);
    return true;
  });
}

describe('base64url 往返', () => {
  it('编解码任意字节保持一致', () => {
    const bytes = new Uint8Array(256);
    for (let i = 0; i < 256; i += 1) bytes[i] = i;
    assert.deepEqual(base64urlToBytes(bytesToBase64url(bytes)), bytes);
  });

  it('拒绝非 base64url 字符', () => {
    expectCode(() => base64urlToBytes('abc.def'), 'HEADER_NOT_BASE64URL');
  });
});

describe('crit 准入', () => {
  it('接受认可集合内且实际出现的扩展', () => {
    const segment = headerSegment({
      alg: 'HS256',
      b64: false,
      crit: ['b64'],
    });
    const result = admitProtectedHeader(segment, { allowedCrit: DEFAULT_ALLOWED_CRIT });
    assert.equal(result.detachedPayloadMode, true);
  });

  it('拒绝验证方不认识的未知 crit 扩展', () => {
    const segment = headerSegment({
      alg: 'HS256',
      'http://example.invalid/hmac-name': 'foo',
      crit: ['http://example.invalid/hmac-name'],
    });
    expectCode(
      () => admitProtectedHeader(segment, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'CRIT_NOT_ALLOWED',
    );
  });

  it('认可集合包含该扩展时放行', () => {
    const segment = headerSegment({
      alg: 'HS256',
      'http://example.invalid/hmac-name': 'foo',
      crit: ['http://example.invalid/hmac-name'],
    });
    const result = admitProtectedHeader(segment, {
      allowedCrit: ['http://example.invalid/hmac-name'],
    });
    assert.equal(result.detachedPayloadMode, false);
  });

  it('crit 为空数组时拒绝', () => {
    expectCode(
      () =>
        admitProtectedHeader(headerSegment({ alg: 'HS256', crit: [] }), {
          allowedCrit: DEFAULT_ALLOWED_CRIT,
        }),
      'CRIT_EMPTY',
    );
  });

  it('crit 不是数组时拒绝', () => {
    expectCode(
      () =>
        admitProtectedHeader(headerSegment({ alg: 'HS256', crit: 'b64' }), {
          allowedCrit: DEFAULT_ALLOWED_CRIT,
        }),
      'CRIT_NOT_ARRAY',
    );
  });

  it('crit 条目不是字符串时拒绝', () => {
    expectCode(
      () =>
        admitProtectedHeader(headerSegment({ alg: 'HS256', crit: [42] }), {
          allowedCrit: DEFAULT_ALLOWED_CRIT,
        }),
      'CRIT_ENTRY_NOT_STRING',
    );
  });

  it('crit 列出的成员未出现在头里时拒绝', () => {
    expectCode(
      () =>
        admitProtectedHeader(headerSegment({ alg: 'HS256', crit: ['b64'] }), {
          allowedCrit: DEFAULT_ALLOWED_CRIT,
        }),
      'CRIT_MEMBER_ABSENT',
    );
  });

  it('crit 包含 RFC 7515 已注册名时拒绝', () => {
    expectCode(
      () =>
        admitProtectedHeader(
          headerSegment({ alg: 'HS256', kid: 'k-1', crit: ['kid'] }),
          { allowedCrit: new Set(['kid']) },
        ),
      'CRIT_REGISTERED_NAME',
    );
  });
});

describe('b64 规则', () => {
  it('b64=false 但 crit 缺失时拒绝', () => {
    const segment = headerSegment({ alg: 'HS256', b64: false });
    expectCode(
      () => admitProtectedHeader(segment, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'B64_WITHOUT_CRIT',
    );
  });

  it('b64=false 但 crit 不含 b64 时拒绝', () => {
    const segment = headerSegment({
      alg: 'HS256',
      b64: false,
      'x-foo': 1,
      crit: ['x-foo'],
    });
    expectCode(
      () => admitProtectedHeader(segment, { allowedCrit: new Set(['x-foo']) }),
      'B64_WITHOUT_CRIT',
    );
  });

  it('b64 不是布尔值时拒绝', () => {
    const segment = headerSegment({ alg: 'HS256', b64: 'false' });
    expectCode(
      () => admitProtectedHeader(segment, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'B64_INVALID_TYPE',
    );
  });

  it('b64=true 视为标准编码载荷且无需进入 crit', () => {
    const result = admitProtectedHeader(
      headerSegment({ alg: 'HS256', b64: true }),
      { allowedCrit: DEFAULT_ALLOWED_CRIT },
    );
    assert.equal(result.detachedPayloadMode, false);
  });
});

describe('重复头成员', () => {
  it('同名成员重复出现时拒绝', () => {
    const raw = '{"alg":"HS256","alg":"none"}';
    const segment = bytesToBase64url(encoder.encode(raw));
    expectCode(
      () => admitProtectedHeader(segment, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'DUPLICATE_HEADER_MEMBER',
    );
  });
});

describe('紧凑序列化与签名输入', () => {
  it('标准模式按 base64url 载荷解码，签名输入为头段+.+载荷段', () => {
    const header = headerSegment({ alg: 'HS256' });
    const payload = bytesToBase64url(encoder.encode('hello world'));
    const signature = bytesToBase64url(encoder.encode('sig-bytes'));
    const compact = `${header}.${payload}.${signature}`;

    const result = admitCompactJws(compact, { allowedCrit: DEFAULT_ALLOWED_CRIT });
    assert.equal(result.detachedPayloadMode, false);
    assert.deepEqual(result.payloadBytes, encoder.encode('hello world'));
    assert.deepEqual(
      result.signingInput,
      encoder.encode(`${header}.${payload}`),
    );
    assert.equal(result.signatureSegment, signature);
  });

  it('b64=false 时载荷原字节进入签名输入，不做 base64url 解码', () => {
    const header = headerSegment({ alg: 'HS256', b64: false, crit: ['b64'] });
    const rawPayload = encoder.encode('{"ledger":"acme"}');
    const payloadText = new TextDecoder().decode(rawPayload);
    const signature = bytesToBase64url(encoder.encode('sig-bytes'));
    const compact = `${header}.${payloadText}.${signature}`;

    const result = admitCompactJws(compact, { allowedCrit: DEFAULT_ALLOWED_CRIT });
    assert.equal(result.detachedPayloadMode, true);
    assert.deepEqual(result.payloadBytes, rawPayload);
    assert.deepEqual(
      result.signingInput,
      encoder.encode(`${header}.${payloadText}`),
    );
  });

  it('b64=false 且载荷含句点时拒绝，防止分段被改变', () => {
    const header = headerSegment({ alg: 'HS256', b64: false, crit: ['b64'] });
    // 攻击者在未编码载荷中塞入句点，制造额外的分段边界。
    const compact = `${header}.a.b.c.${bytesToBase64url(encoder.encode('sig'))}`;
    expectCode(
      () => admitCompactJws(compact, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'PAYLOAD_CONTAINS_PERIOD',
    );
  });

  it('标准模式下载荷含句点同样属于畸形序列化', () => {
    const header = headerSegment({ alg: 'HS256' });
    const compact = `${header}.a.b.${bytesToBase64url(encoder.encode('sig'))}`;
    expectCode(
      () => admitCompactJws(compact, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'COMPACT_MALFORMED',
    );
  });

  it('签名段为空时拒绝', () => {
    const header = headerSegment({ alg: 'HS256' });
    const compact = `${header}.${bytesToBase64url(encoder.encode('p'))}.`;
    expectCode(
      () => admitCompactJws(compact, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'COMPACT_MALFORMED',
    );
  });

  it('标准模式下载荷段非法 base64url 时拒绝', () => {
    const header = headerSegment({ alg: 'HS256' });
    const compact = `${header}.@@@@.${bytesToBase64url(encoder.encode('sig'))}`;
    expectCode(
      () => admitCompactJws(compact, { allowedCrit: DEFAULT_ALLOWED_CRIT }),
      'COMPACT_MALFORMED',
    );
  });
});
