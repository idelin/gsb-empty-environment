import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  RegistrationSecurityError,
  planRegistration,
} from '../src/index.ts';

const CLIENT = 'https://app.example.com/app/page';

describe('默认作用域', () => {
  it('无请求作用域时默认脚本所在目录', () => {
    const plan = planRegistration({
      clientUrl: CLIENT,
      scriptUrl: 'https://app.example.com/app/js/sw.js',
    });
    assert.equal(plan.scope, '/app/js/');
    assert.equal(plan.maxScope, '/app/js/');
    assert.equal(plan.effectiveScriptUrl, '/app/js/sw.js');
  });

  it('脚本目录内的更窄作用域被允许', () => {
    const plan = planRegistration({
      clientUrl: CLIENT,
      scriptUrl: 'https://app.example.com/app/js/sw.js',
      requestedScope: 'https://app.example.com/app/js/sub/',
    });
    assert.equal(plan.scope, '/app/js/sub/');
  });

  it('无头时禁止向脚本目录外扩张', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://app.example.com/app/js/sw.js',
          requestedScope: 'https://app.example.com/app/',
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'SCOPE_OUTSIDE_DEFAULT',
    );
  });
});

describe('Service-Worker-Allowed 头', () => {
  it('祖先目录头允许扩张到该祖先', () => {
    const plan = planRegistration({
      clientUrl: CLIENT,
      scriptUrl: 'https://app.example.com/app/js/sw.js',
      serviceWorkerAllowed: 'https://app.example.com/app/',
      requestedScope: 'https://app.example.com/app/',
    });
    assert.equal(plan.maxScope, '/app/');
    assert.equal(plan.scope, '/app/');
  });

  it('请求作用域超出头范围时拒绝', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://app.example.com/app/deep/js/sw.js',
          serviceWorkerAllowed: 'https://app.example.com/app/deep/',
          requestedScope: 'https://app.example.com/app/',
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'SCOPE_OUTSIDE_ALLOWED_HEADER',
    );
  });

  it('头不能指向脚本目录的无关分支', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://app.example.com/app/js/sw.js',
          serviceWorkerAllowed: 'https://app.example.com/other/',
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'HEADER_SCOPE_NOT_ANCESTOR',
    );
  });

  it('头与脚本不同源时拒绝', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://app.example.com/app/js/sw.js',
          serviceWorkerAllowed: 'https://evil.example.com/app/',
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'CROSS_ORIGIN_SCOPE',
    );
  });

  it('头中的编码分隔符同样被拒绝', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://app.example.com/app/js/sw.js',
          serviceWorkerAllowed: 'https://app.example.com/app%2f../',
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'ENCODED_SEPARATOR_AMBIGUITY',
    );
  });
});

describe('跨来源与重定向脚本', () => {
  it('脚本与客户不同源时拒绝', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://cdn.other.example.com/sw.js',
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'CROSS_ORIGIN_SCRIPT',
    );
  });

  it('同源重定向后以最终 URL 计算默认作用域', () => {
    const plan = planRegistration({
      clientUrl: CLIENT,
      scriptUrl: 'https://app.example.com/old/sw.js',
      redirectChain: [
        'https://app.example.com/moved/sw.js',
        'https://app.example.com/app/v2/sw.js',
      ],
    });
    assert.deepEqual(plan.redirectHops, [
      '/old/sw.js',
      '/moved/sw.js',
      '/app/v2/sw.js',
    ]);
    assert.equal(plan.effectiveScriptUrl, '/app/v2/sw.js');
    assert.equal(plan.defaultScope, '/app/v2/');
    assert.equal(plan.scope, '/app/v2/');
  });

  it('重定向链中任一跳跨源即拒绝', () => {
    assert.throws(
      () =>
        planRegistration({
          clientUrl: CLIENT,
          scriptUrl: 'https://app.example.com/old/sw.js',
          redirectChain: ['https://evil.example.com/app/sw.js'],
        }),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'CROSS_ORIGIN_REDIRECT',
    );
  });

  it('同源重定向脚本的宽作用域仍受最终路径与头约束', () => {
    const plan = planRegistration({
      clientUrl: CLIENT,
      scriptUrl: 'https://app.example.com/old/sw.js',
      redirectChain: ['https://app.example.com/app/v2/sw.js'],
      serviceWorkerAllowed: 'https://app.example.com/app/',
      requestedScope: 'https://app.example.com/app/',
    });
    assert.equal(plan.maxScope, '/app/');
    assert.equal(plan.scope, '/app/');
  });
});
