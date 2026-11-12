import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  type AuthorizationServerRecord,
  type IssuerValidationSession,
  IssuerValidationError,
  parseCallbackParams,
  rememberAuthorizationServer,
  verifyAuthorizationCallback,
  verifyResponseIssuer,
} from '../src/iss-validation.ts';
import { handleAuthorizationCallback } from '../src/callback.ts';

const ISSUER = 'https://honest.example';

function sessionWith(
  record: AuthorizationServerRecord | undefined,
): IssuerValidationSession {
  const session: IssuerValidationSession = {};
  if (record) {
    session.oauthAuthorizationServer = record;
  }
  return session;
}

function supportedSession(
  issuer: string = ISSUER,
  supported = true,
): IssuerValidationSession {
  const session: IssuerValidationSession = {};
  rememberAuthorizationServer(session, {
    issuer,
    authorizationResponseIssParameterSupported: supported,
  });
  return session;
}

function assertRejectsWithCode(fn: () => unknown, code: string): void {
  assert.throws(fn, (error: unknown) => {
    assert.ok(error instanceof IssuerValidationError);
    assert.equal((error as IssuerValidationError).code, code);
    return true;
  });
}

describe('rememberAuthorizationServer', () => {
  it('按原样保存 issuer 字符串和能力位', () => {
    const session: IssuerValidationSession = {};
    rememberAuthorizationServer(session, {
      issuer: 'https://honest.example/path',
      authorizationResponseIssParameterSupported: true,
    });

    assert.deepEqual(session.oauthAuthorizationServer, {
      issuer: 'https://honest.example/path',
      authorizationResponseIssParameterSupported: true,
    });
  });

  it('未提供能力位时规范化为 false', () => {
    const session = supportedSession(ISSUER, false);
    assert.equal(
      session.oauthAuthorizationServer
        ?.authorizationResponseIssParameterSupported,
      false,
    );
  });

  it('issuer 为空字符串时拒绝', () => {
    assert.throws(
      () =>
        rememberAuthorizationServer(sessionWith(undefined)!, { issuer: '' }),
      TypeError,
    );
  });
});

describe('verifyResponseIssuer — 成功与兼容路径', () => {
  it('iss 与会话记录逐字节相等时通过', () => {
    const params = new URLSearchParams(
      `code=abc&iss=${encodeURIComponent(ISSUER)}&state=s1`,
    );
    assert.doesNotThrow(() =>
      verifyResponseIssuer(params, {
        issuer: ISSUER,
        authorizationResponseIssParameterSupported: true,
      }),
    );
  });

  it('iss 缺失且服务器未声明支持时向后兼容放行', () => {
    const params = new URLSearchParams('code=abc&state=s1');
    assert.doesNotThrow(() =>
      verifyResponseIssuer(params, {
        issuer: ISSUER,
        authorizationResponseIssParameterSupported: false,
      }),
    );
  });

  it('iss 缺失且无能力位元数据时同样放行', () => {
    const params = new URLSearchParams('code=abc');
    assert.doesNotThrow(() =>
      verifyResponseIssuer(params, { issuer: ISSUER }),
    );
  });
});

describe('verifyResponseIssuer — 拒绝路径', () => {
  it('iss 缺失但元数据声明支持时拒绝 (missing_iss)', () => {
    const params = new URLSearchParams('code=abc&state=s1');
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(params, {
          issuer: ISSUER,
          authorizationResponseIssParameterSupported: true,
        }),
      'missing_iss',
    );
  });

  it('iss 来自不同授权服务器时拒绝 (iss_mismatch)', () => {
    const params = new URLSearchParams(
      `code=evil-code&iss=${encodeURIComponent('https://malicious.example')}`,
    );
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(params, {
          issuer: ISSUER,
          authorizationResponseIssParameterSupported: true,
        }),
      'iss_mismatch',
    );
  });

  it('尾部斜杠差异不做 URL 规范化，直接拒绝', () => {
    const params = new URLSearchParams(
      `code=abc&iss=${encodeURIComponent(`${ISSUER}/`)}`,
    );
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(params, {
          issuer: ISSUER,
          authorizationResponseIssParameterSupported: true,
        }),
      'iss_mismatch',
    );

    const reverseParams = new URLSearchParams(
      `code=abc&iss=${encodeURIComponent(ISSUER)}`,
    );
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(reverseParams, {
          issuer: `${ISSUER}/`,
          authorizationResponseIssParameterSupported: true,
        }),
      'iss_mismatch',
    );
  });

  it('大小写差异同样按逐字节比较拒绝', () => {
    const params = new URLSearchParams(
      `code=abc&iss=${encodeURIComponent('HTTPS://honest.example')}`,
    );
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(params, {
          issuer: ISSUER,
          authorizationResponseIssParameterSupported: true,
        }),
      'iss_mismatch',
    );
  });

  it('iss 出现多次（值不同）时拒绝 (duplicate_iss)', () => {
    const params = parseCallbackParams(
      `https://client.example/callback?code=abc&iss=${encodeURIComponent(
        ISSUER,
      )}&iss=${encodeURIComponent('https://malicious.example')}`,
    );
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(params, {
          issuer: ISSUER,
          authorizationResponseIssParameterSupported: true,
        }),
      'duplicate_iss',
    );
  });

  it('iss 出现多次（即使值完全相同）也拒绝', () => {
    const params = parseCallbackParams(
      `callback?code=abc&iss=${encodeURIComponent(ISSUER)}&iss=${encodeURIComponent(
        ISSUER,
      )}`,
    );
    assertRejectsWithCode(
      () =>
        verifyResponseIssuer(params, {
          issuer: ISSUER,
          authorizationResponseIssParameterSupported: true,
        }),
      'duplicate_iss',
    );
  });

  it('编码后的 iss 被百分号解码后逐字节比较', () => {
    const params = parseCallbackParams(
      `?iss=${encodeURIComponent('https://honest.example/path/x')}&code=c`,
    );
    assert.doesNotThrow(() =>
      verifyResponseIssuer(params, {
        issuer: 'https://honest.example/path/x',
        authorizationResponseIssParameterSupported: true,
      }),
    );
  });
});

describe('verifyAuthorizationCallback', () => {
  it('会话中没有授权服务器记录时拒绝 (missing_session)', () => {
    assertRejectsWithCode(
      () =>
        verifyAuthorizationCallback(
          '?code=abc&iss=' + encodeURIComponent(ISSUER),
          sessionWith(undefined),
        ),
      'missing_session',
    );
  });

  it('完整 URL、相对路径、URL 对象和 URLSearchParams 均可解析', () => {
    const session = supportedSession();
    const query = 'code=abc&iss=' + encodeURIComponent(ISSUER) + '&state=s1';

    for (const input of [
      `https://client.example/cb?${query}`,
      `/cb?${query}`,
      `?${query}`,
      new URL(`https://client.example/cb?${query}`),
      new URLSearchParams(query),
    ]) {
      const result = verifyAuthorizationCallback(input, session);
      assert.equal(result.params.get('code'), 'abc');
      assert.equal(result.isErrorResponse, false);
      assert.equal(result.record.issuer, ISSUER);
    }
  });

  it('URL 片段被忽略，不参与参数解析', () => {
    const session = supportedSession();
    const result = verifyAuthorizationCallback(
      `https://client.example/cb?code=abc&iss=${encodeURIComponent(
        ISSUER,
      )}#frag`,
      session,
    );
    assert.equal(result.params.get('code'), 'abc');
  });
});

describe('handleAuthorizationCallback — 令牌端点交互顺序', () => {
  it('成功响应在校验通过后才执行令牌交换，且只执行一次', async () => {
    const session = supportedSession();
    let calls = 0;
    const outcome = await handleAuthorizationCallback(
      `?code=good-code&state=s1&iss=${encodeURIComponent(ISSUER)}`,
      session,
      async ({ code, record }) => {
        calls += 1;
        assert.equal(code, 'good-code');
        assert.equal(record.issuer, ISSUER);
        return { access_token: 'token-for-good-code' };
      },
    );

    assert.equal(calls, 1);
    assert.equal(outcome.kind, 'token');
    if (outcome.kind === 'token') {
      assert.deepEqual(outcome.token, {
        access_token: 'token-for-good-code',
      });
    }
  });

  it('iss 缺失但已声明支持：拒绝且绝不调用令牌端点', async () => {
    const session = supportedSession();
    let called = false;

    await assert.rejects(
      handleAuthorizationCallback(
        '?code=evil-code&state=s1',
        session,
        () => {
          called = true;
          return { access_token: 'should-not-exist' };
        },
      ),
      (error: unknown) =>
        error instanceof IssuerValidationError &&
        error.code === 'missing_iss',
    );
    assert.equal(called, false);
  });

  it('iss 不同：拒绝且绝不调用令牌端点（混淆攻击核心用例）', async () => {
    const session = supportedSession();
    let called = false;

    await assert.rejects(
      handleAuthorizationCallback(
        `?code=stolen-code&iss=${encodeURIComponent(
          'https://malicious.example',
        )}`,
        session,
        () => {
          called = true;
          return {};
        },
      ),
      (error: unknown) =>
        error instanceof IssuerValidationError &&
        error.code === 'iss_mismatch',
    );
    assert.equal(called, false);
  });

  it('尾部斜杠差异：拒绝且绝不调用令牌端点', async () => {
    const session = supportedSession();
    let called = false;

    await assert.rejects(
      handleAuthorizationCallback(
        `?code=stolen-code&iss=${encodeURIComponent(`${ISSUER}/`)}`,
        session,
        () => {
          called = true;
          return {};
        },
      ),
      (error: unknown) =>
        error instanceof IssuerValidationError &&
        error.code === 'iss_mismatch',
    );
    assert.equal(called, false);
  });

  it('重复 iss：拒绝且绝不调用令牌端点', async () => {
    const session = supportedSession();
    let called = false;

    await assert.rejects(
      handleAuthorizationCallback(
        `?code=stolen-code&iss=${encodeURIComponent(
          ISSUER,
        )}&iss=${encodeURIComponent(ISSUER)}`,
        session,
        () => {
          called = true;
          return {};
        },
      ),
      (error: unknown) =>
        error instanceof IssuerValidationError &&
        error.code === 'duplicate_iss',
    );
    assert.equal(called, false);
  });

  it('无会话记录：拒绝且绝不调用令牌端点', async () => {
    let called = false;

    await assert.rejects(
      handleAuthorizationCallback(
        `?code=x&iss=${encodeURIComponent(ISSUER)}`,
        sessionWith(undefined),
        () => {
          called = true;
          return {};
        },
      ),
      (error: unknown) =>
        error instanceof IssuerValidationError &&
        error.code === 'missing_session',
    );
    assert.equal(called, false);
  });
});

describe('RFC 9207 5.2 — 错误响应中的 iss 校验', () => {
  it('错误响应带正确 iss：校验通过并返回错误，不触发令牌交换', async () => {
    const session = supportedSession();
    let called = false;

    const outcome = await handleAuthorizationCallback(
      `?error=access_denied&error_description=user%20denied&state=s1&iss=${encodeURIComponent(
        ISSUER,
      )}`,
      session,
      () => {
        called = true;
        return {};
      },
    );

    assert.equal(called, false);
    assert.equal(outcome.kind, 'error_response');
    if (outcome.kind === 'error_response') {
      assert.equal(outcome.error, 'access_denied');
      assert.equal(outcome.errorDescription, 'user denied');
      assert.equal(outcome.record.issuer, ISSUER);
    }
  });

  it('错误响应 iss 缺失但服务器声明支持：拒绝', () => {
    const session = supportedSession();
    assertRejectsWithCode(
      () =>
        verifyAuthorizationCallback(
          '?error=access_denied&state=s1',
          session,
        ),
      'missing_iss',
    );
  });

  it('错误响应 iss 与记录不符：拒绝', () => {
    const session = supportedSession();
    assertRejectsWithCode(
      () =>
        verifyAuthorizationCallback(
          `?error=access_denied&iss=${encodeURIComponent(
            'https://malicious.example',
          )}`,
          session,
        ),
      'iss_mismatch',
    );
  });

  it('错误响应重复 iss：拒绝', () => {
    const session = supportedSession();
    assertRejectsWithCode(
      () =>
        verifyAuthorizationCallback(
          `?error=access_denied&iss=${encodeURIComponent(
            ISSUER,
          )}&iss=${encodeURIComponent(ISSUER)}`,
          session,
        ),
      'duplicate_iss',
    );
  });

  it('错误响应 iss 缺失且服务器未声明支持：兼容放行并返回错误', async () => {
    const session = supportedSession(ISSUER, false);
    let called = false;

    const outcome = await handleAuthorizationCallback(
      '?error=access_denied&state=s1',
      session,
      () => {
        called = true;
        return {};
      },
    );

    assert.equal(called, false);
    assert.equal(outcome.kind, 'error_response');
    if (outcome.kind === 'error_response') {
      assert.equal(outcome.error, 'access_denied');
    }
  });
});
