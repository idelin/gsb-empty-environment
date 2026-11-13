import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  admitJweHeader,
  admitJweHeaderOrThrow,
  decodeBase64Url,
  JweHeaderAdmissionError,
  MAX_P2S_BYTES,
  MIN_P2S_BYTES,
  parseProtectedHeader,
} from './index.ts';

const BUDGET = {
  maxP2c: 100_000,
  maxDecompressedBytes: 1_000_000,
} as const;

function b64url(input: number | Uint8Array | string): string {
  const data =
    typeof input === 'number'
      ? new Uint8Array(input).fill(0x41)
      : typeof input === 'string'
        ? Buffer.from(input)
        : input;
  return Buffer.from(data).toString('base64url');
}

function pbes2Header(overrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    alg: 'PBES2-HS256+A128KW',
    enc: 'A128CBC-HS256',
    p2c: 10_000,
    p2s: b64url(16),
    ...overrides,
  });
}

describe('PBES2 p2c 准入', () => {
  it('拒绝 p2c 为十亿（超出调用方上限）', () => {
    const result = admitJweHeader(pbes2Header({ p2c: 1_000_000_000 }), 1024, BUDGET);
    assert.equal(result.admitted, false);
    assert.equal(result.code, 'P2C_OUT_OF_RANGE');
    assert.equal(result.parameter, 'p2c');
  });

  it('拒绝非整数 p2c（小数、字符串、布尔）', () => {
    for (const bad of [1000.5, '10000', true]) {
      const result = admitJweHeader(pbes2Header({ p2c: bad }), 1024, BUDGET);
      assert.equal(result.admitted, false);
      assert.equal(result.code, 'P2C_NOT_INTEGER');
    }
  });

  it('拒绝低于 1000 的 p2c，接受 1000 与上限两个边界', () => {
    assert.equal(
      admitJweHeader(pbes2Header({ p2c: 999 }), 1024, BUDGET).code,
      'P2C_OUT_OF_RANGE',
    );
    for (const p2c of [1000, BUDGET.maxP2c]) {
      const result = admitJweHeader(pbes2Header({ p2c }), 1024, BUDGET);
      assert.equal(result.admitted, true, `p2c=${p2c} 应被接受`);
      assert.equal(result.limits?.p2c.min, 1000);
      assert.equal(result.limits?.p2c.max, BUDGET.maxP2c);
    }
  });

  it('拒绝缺失的 p2c / p2s', () => {
    const noP2c = admitJweHeader(
      JSON.stringify({ alg: 'PBES2-HS512+A256KW', p2s: b64url(16) }),
      1024,
      BUDGET,
    );
    assert.equal(noP2c.code, 'P2C_MISSING');

    const noP2s = admitJweHeader(
      JSON.stringify({ alg: 'PBES2-HS512+A256KW', p2c: 5000 }),
      1024,
      BUDGET,
    );
    assert.equal(noP2s.code, 'P2S_MISSING');
  });

  it('三种 PBES2 算法都执行同一套 p2c/p2s 校验', () => {
    for (const alg of [
      'PBES2-HS256+A128KW',
      'PBES2-HS384+A192KW',
      'PBES2-HS512+A256KW',
    ]) {
      assert.equal(admitJweHeader(pbes2Header({ alg }), 1024, BUDGET).admitted, true);
      assert.equal(
        admitJweHeader(pbes2Header({ alg, p2c: 1_000_000_000 }), 1024, BUDGET).code,
        'P2C_OUT_OF_RANGE',
      );
    }
  });
});

describe('PBES2 p2s 准入', () => {
  it('拒绝解码后只有 4 字节的 p2s', () => {
    const result = admitJweHeader(pbes2Header({ p2s: b64url(4) }), 1024, BUDGET);
    assert.equal(result.admitted, false);
    assert.equal(result.code, 'P2S_LENGTH_OUT_OF_RANGE');
  });

  it('接受 8 与 64 字节边界，拒绝 65 字节', () => {
    assert.equal(
      admitJweHeader(pbes2Header({ p2s: b64url(MIN_P2S_BYTES) }), 1024, BUDGET).admitted,
      true,
    );
    assert.equal(
      admitJweHeader(pbes2Header({ p2s: b64url(MAX_P2S_BYTES) }), 1024, BUDGET).admitted,
      true,
    );
    assert.equal(
      admitJweHeader(pbes2Header({ p2s: b64url(MAX_P2S_BYTES + 1) }), 1024, BUDGET).code,
      'P2S_LENGTH_OUT_OF_RANGE',
    );
  });

  it('拒绝非 base64url 的 p2s（填充、+、/、空串）', () => {
    for (const bad of [b64url(16) + '=', 'abcd+efg', 'abcd/efg', '']) {
      const result = admitJweHeader(pbes2Header({ p2s: bad }), 1024, BUDGET);
      assert.equal(result.admitted, false);
      assert.equal(result.code, 'P2S_NOT_BASE64URL');
    }
  });

  it('decodeBase64Url 正确还原字节', () => {
    const salt = new Uint8Array([0, 1, 2, 253, 254, 255, 7, 63]);
    assert.deepEqual(decodeBase64Url(b64url(salt)), salt);
  });
});

describe('zip 与压缩炸弹准入', () => {
  it('拒绝超过 100 倍的压缩炸弹（1KB 输入声明 100 万输出 = 1000 倍）', () => {
    const result = admitJweHeader(pbes2Header({ zip: 'DEF' }), 1024, BUDGET);
    assert.equal(result.admitted, false);
    assert.equal(result.code, 'COMPRESSION_RATIO_EXCEEDED');
  });

  it('恰好 100 倍允许，超过即拒绝', () => {
    const header = pbes2Header({ zip: 'DEF' });
    const budget = { maxP2c: 100_000, maxDecompressedBytes: 10_000 };
    assert.equal(admitJweHeader(header, 100, budget).admitted, true);
    assert.equal(
      admitJweHeader(header, 99, budget).code,
      'COMPRESSION_RATIO_EXCEEDED',
    );
  });

  it('zip 只允许缺省或 DEF', () => {
    assert.equal(
      admitJweHeader(pbes2Header({ zip: 'GZIP' }), 1024, BUDGET).code,
      'ZIP_NOT_ALLOWED',
    );
    assert.equal(admitJweHeader(pbes2Header(), 999_999, BUDGET).admitted, true);
    assert.equal(
      admitJweHeader(pbes2Header({ zip: 'DEF' }), 10_000, BUDGET).admitted,
      true,
    );
  });

  it('解压预算为 0 时任何 DEF 输入都无法通过', () => {
    const result = admitJweHeader(
      pbes2Header({ zip: 'DEF' }),
      1,
      { maxP2c: 100_000, maxDecompressedBytes: 0 },
    );
    assert.equal(result.code, 'DECOMPRESSED_BUDGET_ZERO');
  });

  it('拒绝非法压缩载荷长度', () => {
    assert.equal(
      admitJweHeader(pbes2Header({ zip: 'DEF' }), -1, BUDGET).code,
      'INVALID_COMPRESSED_LENGTH',
    );
  });
});

describe('重复头参数名', () => {
  it('解析阶段拒绝顶层重复键', () => {
    const header = '{"alg":"PBES2-HS256+A128KW","p2c":10000,"p2c":1}';
    assert.throws(
      () => parseProtectedHeader(header),
      (error: unknown) =>
        error instanceof JweHeaderAdmissionError &&
        error.code === 'DUPLICATE_HEADER_PARAMETER' &&
        error.parameter === 'p2c',
    );
    const result = admitJweHeader(header, 1024, BUDGET);
    assert.equal(result.admitted, false);
    assert.equal(result.code, 'DUPLICATE_HEADER_PARAMETER');
  });

  it('拒绝嵌套对象中的重复键，但不影响其他无重复头', () => {
    const nested = '{"alg":"PBES2-HS256+A128KW","epk":{"kty":"EC","kty":"RSA"}}';
    assert.equal(
      admitJweHeader(nested, 1024, BUDGET).code,
      'DUPLICATE_HEADER_PARAMETER',
    );
    assert.equal(admitJweHeader(pbes2Header(), 1024, BUDGET).admitted, true);
  });

  it('数组内重复值不算对象重复键，由 crit 规则处理', () => {
    const header = JSON.stringify({
      alg: 'PBES2-HS256+A128KW',
      p2c: 10_000,
      p2s: b64url(16),
      crit: ['x-foo', 'x-foo'],
      'x-foo': 1,
    });
    const result = admitJweHeader(header, 1024, {
      ...BUDGET,
      understoodCriticalHeaders: ['x-foo'],
    });
    assert.equal(result.code, 'CRIT_DUPLICATE');
  });
});

describe('禁用 alg', () => {
  it('拒绝 RSA1_5 与 dir', () => {
    for (const alg of ['dir', 'RSA1_5']) {
      const result = admitJweHeader(
        JSON.stringify({ alg, enc: 'A128GCM' }),
        1024,
        BUDGET,
      );
      assert.equal(result.admitted, false);
      assert.equal(result.code, 'ALG_FORBIDDEN');
      assert.equal(result.parameter, 'alg');
    }
  });

  it('拒绝缺失 alg', () => {
    const result = admitJweHeader(
      JSON.stringify({ enc: 'A128GCM' }),
      1024,
      BUDGET,
    );
    assert.equal(result.code, 'ALG_MISSING');
  });

  it('非 PBES2、非禁用算法不强制 p2c/p2s', () => {
    const result = admitJweHeader(
      JSON.stringify({ alg: 'ECDH-ES+A128KW', enc: 'A128GCM' }),
      1024,
      BUDGET,
    );
    assert.equal(result.admitted, true);
  });
});

describe('crit 准入', () => {
  it('未被理解的 crit 名称被拒绝', () => {
    const header = JSON.stringify({
      alg: 'PBES2-HS256+A128KW',
      p2c: 10_000,
      p2s: b64url(16),
      crit: ['x-tenant'],
      'x-tenant': 'acme',
    });
    assert.equal(
      admitJweHeader(header, 1024, BUDGET).code,
      'CRIT_NAME_NOT_UNDERSTOOD',
    );
  });

  it('调用方声明理解后放行；注册参数名出现在 crit 中拒绝', () => {
    const header = JSON.stringify({
      alg: 'PBES2-HS256+A128KW',
      p2c: 10_000,
      p2s: b64url(16),
      crit: ['x-tenant'],
      'x-tenant': 'acme',
    });
    const ok = admitJweHeader(header, 1024, {
      ...BUDGET,
      understoodCriticalHeaders: ['x-tenant'],
    });
    assert.equal(ok.admitted, true);

    const critKid = pbes2Header({ crit: ['kid'], kid: 'k1' });
    assert.equal(admitJweHeader(critKid, 1024, BUDGET).code, 'CRIT_REGISTERED_NAME');
  });

  it('crit 名称缺席、空数组、非数组均拒绝', () => {
    const absent = pbes2Header({ crit: ['x-missing'] });
    assert.equal(
      admitJweHeader(absent, 1024, {
        ...BUDGET,
        understoodCriticalHeaders: ['x-missing'],
      }).code,
      'CRIT_NAME_ABSENT',
    );
    assert.equal(
      admitJweHeader(pbes2Header({ crit: [] }), 1024, BUDGET).code,
      'CRIT_EMPTY',
    );
    assert.equal(
      admitJweHeader(pbes2Header({ crit: 'kid' }), 1024, BUDGET).code,
      'CRIT_NOT_ARRAY',
    );
  });
});

describe('compact 头、预算与异常接口', () => {
  it('接受 base64url 编码的 JWE 受保护头（compact 第一段）', () => {
    const json = pbes2Header();
    const compact = Buffer.from(json).toString('base64url');
    const result = admitJweHeader(compact, 1024, BUDGET);
    assert.equal(result.admitted, true);
  });

  it('compact 头中的重复键同样被拒绝', () => {
    const duplicated = '{"alg":"PBES2-HS256+A128KW","p2c":10000,"p2c":1000}';
    const compact = Buffer.from(duplicated).toString('base64url');
    const result = admitJweHeader(compact, 1024, BUDGET);
    assert.equal(result.code, 'DUPLICATE_HEADER_PARAMETER');
  });

  it('非法 JSON 与非对象头被拒绝', () => {
    assert.equal(admitJweHeader('{not-json', 1024, BUDGET).code, 'INVALID_JSON');
    assert.equal(admitJweHeader('[1,2,3]', 1024, BUDGET).code, 'HEADER_NOT_OBJECT');
  });

  it('非法预算被拒绝', () => {
    assert.equal(
      admitJweHeader(pbes2Header(), 1024, {
        maxP2c: 999,
        maxDecompressedBytes: 1,
      }).code,
      'BUDGET_INVALID',
    );
    assert.equal(
      admitJweHeader(pbes2Header(), 1024, {
        maxP2c: 10_000,
        maxDecompressedBytes: -1,
      }).code,
      'BUDGET_INVALID',
    );
  });

  it('admitJweHeaderOrThrow 抛出带 code 的 JweHeaderAdmissionError', () => {
    assert.throws(
      () =>
        admitJweHeaderOrThrow(
          JSON.stringify({ alg: 'RSA1_5' }),
          1024,
          BUDGET,
        ),
      (error: unknown) =>
        error instanceof JweHeaderAdmissionError && error.code === 'ALG_FORBIDDEN',
    );
  });

  it('整个校验过程不触碰 PBKDF2：准入通过后才允许调用方派生', () => {
    const result = admitJweHeader(pbes2Header(), 1024, BUDGET);
    assert.equal(result.admitted, true);
    assert.deepEqual(result.limits, {
      p2c: { min: 1000, max: 100_000 },
      p2sBytes: { min: 8, max: 64 },
      maxCompressionRatio: 100,
      maxDecompressedBytes: 1_000_000,
    });
  });
});
