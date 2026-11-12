import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  DocumentRestoreController,
  type PermissionState,
  type RestoreHost,
} from '../index.ts';
import { DocumentRestoreError } from '../src/errors.ts';

const CAPABILITY = 'clipboard-write';

class FakeHost implements RestoreHost {
  private permissions = new Map<string, PermissionState>();
  readonly taskCalls: string[] = [];
  readonly queries: string[] = [];

  setPermission(capability: string, state: PermissionState): void {
    this.permissions.set(capability, state);
  }

  async queryPermission(capability: string): Promise<PermissionState> {
    this.queries.push(capability);
    return this.permissions.get(capability) ?? 'prompt';
  }

  async runProtectedTask(operationId: string): Promise<void> {
    this.taskCalls.push(operationId);
  }
}

function setup(): {
  controller: DocumentRestoreController;
  host: FakeHost;
} {
  const host = new FakeHost();
  host.setPermission(CAPABILITY, 'granted');
  const controller = new DocumentRestoreController(host);
  return { controller, host };
}

test('许可仍有效且代次未变时，恢复后解冻受保护任务并恢复显示状态', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);

  const snapshot = controller.freeze({ scrollY: 420, draft: 'hello' });
  assert.equal(controller.isFrozen(), true);

  const report = await controller.restore(snapshot);

  assert.deepEqual(report.resumed, ['op-1']);
  assert.deepEqual(report.blocked, []);
  assert.equal(report.duplicate, false);
  assert.deepEqual(report.displayState, { scrollY: 420, draft: 'hello' });
  assert.deepEqual(host.taskCalls, ['op-1']);
  assert.ok(host.queries.includes(CAPABILITY));
  assert.equal(controller.isFrozen(), false);
});

test('用户在冻结前取消的操作，恢复时不会重新调度', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  controller.cancel('op-1');

  const snapshot = controller.freeze();
  const report = await controller.restore(snapshot);

  assert.deepEqual(report.resumed, []);
  assert.deepEqual(report.skippedCancelled, ['op-1']);
  assert.deepEqual(host.taskCalls, []);
});

test('后台撤销许可后，恢复只恢复显示状态，受保护任务被阻止', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  const snapshot = controller.freeze({ scrollY: 100 });

  // 页面在 bfcache 期间，宿主在后台撤销了临时权限。
  controller.revokeCapability(CAPABILITY);
  host.setPermission(CAPABILITY, 'denied');

  const report = await controller.restore(snapshot);

  assert.deepEqual(report.resumed, []);
  assert.deepEqual(report.displayState, { scrollY: 100 });
  assert.equal(report.blocked.length, 1);
  assert.equal(report.blocked[0].operationId, 'op-1');
  assert.ok(
    report.blocked[0].reason === 'permission-revoked' ||
      report.blocked[0].reason === 'generation-changed',
  );
  assert.deepEqual(host.taskCalls, []);
});

test('许可仍为 granted 但代次已推进时，拒绝凭旧快照解冻', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  const snapshot = controller.freeze();

  // 后台先撤销又重新授予：状态回到 granted，但代次已变化。
  controller.revokeCapability(CAPABILITY);
  controller.recordGeneration(CAPABILITY, 2);
  host.setPermission(CAPABILITY, 'granted');

  const report = await controller.restore(snapshot);

  assert.deepEqual(report.resumed, []);
  assert.equal(report.blocked[0].reason, 'generation-changed');
  assert.deepEqual(host.taskCalls, []);
});

test('重复恢复事件不会重发任务，旧快照无法重新取得能力', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  const snapshot = controller.freeze();

  const first = await controller.restore(snapshot);
  assert.deepEqual(first.resumed, ['op-1']);

  // 浏览器对同一历史条目再次派发 pageshow（重复恢复事件）。
  const second = await controller.restore(snapshot);

  assert.equal(second.duplicate, true);
  assert.deepEqual(second.resumed, []);
  assert.deepEqual(second.blocked, []);
  assert.deepEqual(host.taskCalls, ['op-1']);
});

test('冻结前已消费的操作，恢复时绝不重发', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  controller.markConsumed('op-1');

  const snapshot = controller.freeze();
  const report = await controller.restore(snapshot);

  assert.deepEqual(report.resumed, []);
  assert.deepEqual(report.skippedConsumed, ['op-1']);
  assert.deepEqual(host.taskCalls, []);
});

test('冻结期间关闭变更入口，无法篡改或取消待处理操作', () => {
  const { controller } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  controller.freeze();

  assert.throws(
    () => controller.cancel('op-1'),
    (error: unknown) =>
      error instanceof DocumentRestoreError &&
      error.code === 'CONTROLLER_FROZEN',
  );
  assert.throws(
    () => controller.markConsumed('op-1'),
    (error: unknown) =>
      error instanceof DocumentRestoreError &&
      error.code === 'CONTROLLER_FROZEN',
  );
  assert.throws(
    () => controller.registerPending('op-2', CAPABILITY, 1),
    (error: unknown) =>
      error instanceof DocumentRestoreError &&
      error.code === 'CONTROLLER_FROZEN',
  );
});

test('快照为不可变纯数据，篡改快照不会影响恢复判定', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  const snapshot = controller.freeze();

  assert.throws(() => {
    // 运行时冻结应拒绝写入；类型层面该字段本身也是 readonly。
    (snapshot as { snapshotId: string }).snapshotId = 'forged';
  }, TypeError);
  assert.throws(() => {
    (
      snapshot.permissionGenerations as Record<string, number>
    )[CAPABILITY] = 999;
  }, TypeError);

  const report = await controller.restore(snapshot);
  assert.deepEqual(report.resumed, ['op-1']);
  assert.deepEqual(host.taskCalls, ['op-1']);
});

test('恢复完成后可重新冻结，新一轮恢复独立复核当前许可', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  const firstSnapshot = controller.freeze();
  await controller.restore(firstSnapshot);

  // 旧快照的重复事件仍被拒绝。
  const stale = await controller.restore(firstSnapshot);
  assert.equal(stale.duplicate, true);

  // 新生命周期：新操作、新快照；此时后台已撤销许可。
  controller.revokeCapability(CAPABILITY);
  host.setPermission(CAPABILITY, 'denied');
  controller.registerPending('op-2', CAPABILITY, controller.getGeneration(CAPABILITY)!);
  const secondSnapshot = controller.freeze();
  const report = await controller.restore(secondSnapshot);

  assert.equal(report.duplicate, false);
  assert.deepEqual(report.resumed, []);
  assert.equal(report.blocked[0].operationId, 'op-2');
  assert.deepEqual(host.taskCalls, ['op-1']);
});

test('blocked 事件随每个被阻止的操作派发', async () => {
  const { controller, host } = setup();
  controller.registerPending('op-1', CAPABILITY, 1);
  const events: string[] = [];
  controller.on('blocked', (event) => events.push(event.operationId));

  const snapshot = controller.freeze();
  controller.revokeCapability(CAPABILITY);
  host.setPermission(CAPABILITY, 'denied');
  await controller.restore(snapshot);

  assert.deepEqual(events, ['op-1']);
});
