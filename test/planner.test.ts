import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { RegistrationPlanner } from '../src/index.ts';

const ORIGIN = 'https://app.example.com';

function register(
  planner: RegistrationPlanner,
  scriptPath: string,
  requestedScope?: string,
  serviceWorkerAllowed?: string,
) {
  return planner.register({
    clientUrl: `${ORIGIN}/page`,
    scriptUrl: `${ORIGIN}${scriptPath}`,
    requestedScope: requestedScope === undefined ? undefined : `${ORIGIN}${requestedScope}`,
    serviceWorkerAllowed:
      serviceWorkerAllowed === undefined ? undefined : `${ORIGIN}${serviceWorkerAllowed}`,
  });
}

describe('RegistrationPlanner 范围重叠', () => {
  it('首个注册为 create 且无重叠', () => {
    const planner = new RegistrationPlanner();
    const decision = register(planner, '/app/js/sw.js');
    assert.equal(decision.action, 'create');
    assert.deepEqual(decision.overlaps, []);
    assert.equal(decision.supersededId, null);
  });

  it('不相交作用域各自独立', () => {
    const planner = new RegistrationPlanner();
    register(planner, '/app/a/sw.js', '/app/a/');
    const second = register(planner, '/app/b/sw.js', '/app/b/');
    assert.equal(second.action, 'create');
    assert.deepEqual(second.overlaps, []);
    assert.equal(planner.list().length, 2);
  });

  it('嵌套宽作用域报告 contains 重叠但保留两条注册', () => {
    const planner = new RegistrationPlanner();
    const narrow = register(
      planner,
      '/app/narrow/js/sw.js',
      '/app/narrow/',
      '/app/narrow/',
    );
    const wide = register(
      planner,
      '/app/sw.js',
      '/app/',
      '/app/',
    );
    assert.equal(wide.action, 'create');
    assert.equal(wide.overlaps.length, 1);
    assert.equal(wide.overlaps[0].relation, 'contains');
    assert.equal(wide.overlaps[0].existingId, narrow.registration.id);
    assert.equal(planner.list().length, 2);
  });

  it('更窄注册相对已有宽作用域报告 contained-by', () => {
    const planner = new RegistrationPlanner();
    register(planner, '/app/sw.js', '/app/', '/app/');
    const nested = register(planner, '/app/narrow/sw.js', '/app/narrow/');
    assert.equal(nested.overlaps[0].relation, 'contained-by');
  });

  it('相同作用域走 update 并替换旧注册', () => {
    const planner = new RegistrationPlanner();
    const first = register(planner, '/app/sw.js', '/app/', '/app/');
    const second = register(planner, '/app/sw.js', '/app/', '/app/');
    assert.equal(second.action, 'update');
    assert.equal(second.supersededId, first.registration.id);
    assert.equal(planner.list().length, 1);
    assert.equal(planner.list()[0].id, second.registration.id);
  });

  it('匹配客户 URL 时选择最具体的作用域', () => {
    const planner = new RegistrationPlanner();
    register(planner, '/app/sw.js', '/app/', '/app/');
    const narrow = register(planner, '/app/narrow/sw.js', '/app/narrow/');
    const match = planner.findRegistration(`${ORIGIN}/app/narrow/page`);
    assert.equal(match?.id, narrow.registration.id);

    const wideMatch = planner.findRegistration(`${ORIGIN}/app/other/page`);
    assert.equal(wideMatch?.scope, '/app/');
    assert.equal(planner.findRegistration(`${ORIGIN}/elsewhere/x`), null);
  });

  it('字符串前缀相似但段不同的作用域不构成重叠', () => {
    const planner = new RegistrationPlanner();
    register(planner, '/app/sw.js', '/app/', '/app/');
    const decision = register(planner, '/application/sw.js', '/application/');
    assert.deepEqual(decision.overlaps, []);
    assert.equal(planner.findRegistration(`${ORIGIN}/application/x`)?.scope, '/application/');
  });
});
