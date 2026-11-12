import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  RegistrationModel,
  RegistrationSecurityError,
} from '../src/index.ts';

const ORIGIN = 'https://app.example.com';

function install(model: RegistrationModel, scope = '/app/', skipWaiting = false) {
  const handle = model.startInstall({
    origin: ORIGIN,
    scope,
    scriptUrl: `${ORIGIN}${scope}sw.js`,
    skipWaiting,
  });
  return handle.complete();
}

describe('首次安装', () => {
  it('无现存客户时安装完成直接 active', () => {
    const model = new RegistrationModel();
    const version = install(model);
    assert.equal(version.state, 'active');
  });

  it('新客户由 active 版本控制', () => {
    const model = new RegistrationModel();
    const version = install(model);
    const client = model.openClient('c1', `${ORIGIN}/app/page`);
    assert.equal(client.controllerId, version.id);
  });
});

describe('新版本激活与旧客户控制权分离', () => {
  it('新版本安装后停在 waiting，不抢占旧客户', () => {
    const model = new RegistrationModel();
    const first = install(model);
    model.openClient('c1', `${ORIGIN}/app/page`);

    const secondHandle = model.startInstall({
      origin: ORIGIN,
      scope: '/app/',
      scriptUrl: `${ORIGIN}/app/sw.js`,
    });
    const second = secondHandle.complete();

    assert.equal(second.state, 'waiting');
    assert.equal(first.state, 'active');
    assert.equal(model.getClient('c1').controllerId, first.id);
    assert.equal(model.canActivate(second.id), false);
  });

  it('旧客户未卸载时强制激活被拒绝', () => {
    const model = new RegistrationModel();
    install(model);
    model.openClient('c1', `${ORIGIN}/app/page`);
    const second = install(model);

    assert.throws(
      () => model.activate(second.id),
      (error: unknown) =>
        error instanceof RegistrationSecurityError && error.code === 'BAD_STATE',
    );
    assert.equal(second.state, 'waiting');
    assert.equal(model.getClient('c1').controllerId !== second.id, true);
  });

  it('旧客户全部卸载后允许激活，旧版本变 redundant', () => {
    const model = new RegistrationModel();
    const first = install(model);
    model.openClient('c1', `${ORIGIN}/app/page`);
    const second = install(model);

    model.unloadClient('c1');
    assert.equal(model.canActivate(second.id), true);
    const result = model.activate(second.id);
    assert.equal(result.activatedVersionId, second.id);
    assert.equal(result.replacedVersionId, first.id);
    assert.equal(second.state, 'active');
    assert.equal(first.state, 'redundant');
  });

  it('force 激活只切换控制器版本，不静默改写存量客户 controller', () => {
    const model = new RegistrationModel();
    const first = install(model);
    model.openClient('c1', `${ORIGIN}/app/page`);
    const second = install(model);

    model.activate(second.id, { force: true });
    assert.equal(second.state, 'active');
    assert.equal(first.state, 'redundant');
    assert.equal(model.getClient('c1').controllerId, first.id);

    const claimed = model.claim(second.id);
    assert.deepEqual(claimed, ['c1']);
    assert.equal(model.getClient('c1').controllerId, second.id);
  });

  it('导航到作用域内页面才切换到新 active 版本', () => {
    const model = new RegistrationModel();
    const first = install(model);
    model.openClient('c1', `${ORIGIN}/app/page`);
    const second = install(model);
    model.activate(second.id, { force: true });

    assert.equal(model.getClient('c1').controllerId, first.id);
    const navigated = model.navigateClient('c1', `${ORIGIN}/app/other`);
    assert.equal(navigated.controllerId, second.id);
  });

  it('skipWaiting 版本可在仍有旧客户时激活', () => {
    const model = new RegistrationModel();
    install(model);
    model.openClient('c1', `${ORIGIN}/app/page`);
    const handle = model.startInstall({
      origin: ORIGIN,
      scope: '/app/',
      scriptUrl: `${ORIGIN}/app/sw.js`,
      skipWaiting: true,
    });
    const waiting = handle.complete();
    assert.equal(model.canActivate(waiting.id), true);
    model.activate(waiting.id);
    assert.equal(waiting.state, 'active');
    assert.equal(model.getClient('c1').controllerId !== waiting.id, true);
  });
});

describe('迟到安装完成', () => {
  it('安装中的旧版本被新版本取代后，迟到完成被拒绝并保持 redundant', () => {
    const model = new RegistrationModel();
    install(model);

    const slow = model.startInstall({
      origin: ORIGIN,
      scope: '/app/',
      scriptUrl: `${ORIGIN}/app/sw.js`,
    });
    const replacement = model.startInstall({
      origin: ORIGIN,
      scope: '/app/',
      scriptUrl: `${ORIGIN}/app/sw.js?v=2`,
    });

    assert.equal(model.getVersion(slow.versionId).state, 'redundant');
    assert.throws(
      () => slow.complete(),
      (error: unknown) =>
        error instanceof RegistrationSecurityError && error.code === 'BAD_STATE',
    );
    assert.equal(model.getVersion(slow.versionId).state, 'redundant');

    const done = replacement.complete();
    assert.equal(done.state, 'waiting');
  });

  it('waiting 旧版本被新安装取代时也变为 redundant', () => {
    const model = new RegistrationModel();
    install(model);
    const waitingVersion = install(model);
    assert.equal(waitingVersion.state, 'waiting');

    const next = model.startInstall({
      origin: ORIGIN,
      scope: '/app/',
      scriptUrl: `${ORIGIN}/app/sw.js?v=3`,
    });
    assert.equal(model.getVersion(waitingVersion.id).state, 'redundant');
    next.complete();
    assert.equal(model.getVersion(waitingVersion.id).state, 'redundant');
  });

  it('失败的安装进入 redundant 且不影响 active 版本', () => {
    const model = new RegistrationModel();
    const first = install(model);
    const handle = model.startInstall({
      origin: ORIGIN,
      scope: '/app/',
      scriptUrl: `${ORIGIN}/app/bad.js`,
    });
    const failed = handle.fail();
    assert.equal(failed.state, 'redundant');
    assert.equal(model.getVersion(first.id).state, 'active');
  });
});
