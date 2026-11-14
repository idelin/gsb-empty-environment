import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CredentialVault,
  CredentialAccessDenied,
  inputSink,
  DEFAULT_POLICY,
} from '../index.ts';
import type { CallerContext } from '../index.ts';

/** 合法第一方、手势内、受保护上下文的基线。 */
function trustedCtx(overrides: Partial<CallerContext> = {}): CallerContext {
  return {
    secureContext: true,
    userActivation: true,
    sameOriginFrame: true,
    firstPartyScript: true,
    policyAllows: true,
    gestureId: `gesture-${Math.random()}`,
    ...overrides,
  };
}

/** 模拟 DOM <input>：IDL value 为原型访问器（不可枚举），与 content attribute 分离。 */
class MockInput {
  #value = '';
  #attrs = new Map<string, string>();
  get value() { return this.#value; }
  set value(v: string) { this.#value = v; }
  setAttribute(name: string, v: string) { this.#attrs.set(name, v); }
  getAttribute(name: string) { return this.#attrs.get(name) ?? null; }
  get outerHTML() {
    const attrs = [...this.#attrs].map(([k, v]) => ` ${k}="${v}"`).join('');
    return `<input${attrs}>`;
  }
}

const SECRET = 'Sup3r$ecret!Passw0rd';

test('合法路径：存储后可在用户手势 + 受保护上下文中读取', async () => {
  const vault = await CredentialVault.create();
  await vault.store('login', SECRET);
  const out = await vault.reveal('login', trustedCtx());
  assert.equal(out, SECRET);
});

test('脚本读取：第三方注入脚本无权读取，且被拒绝审计', async () => {
  const vault = await CredentialVault.create();
  await vault.store('login', SECRET);
  await assert.rejects(
    vault.reveal('login', trustedCtx({ firstPartyScript: false })),
    (e: unknown) => e instanceof CredentialAccessDenied && e.reason === 'third-party-script',
  );
  const denies = vault.audit.entries().filter((e) => e.action === 'deny');
  assert.equal(denies.length, 1);
  assert.equal(denies[0].reason, 'third-party-script');
});

test('跨源框架：iframe 内调用被拒绝', async () => {
  const vault = await CredentialVault.create();
  await vault.store('login', SECRET);
  await assert.rejects(
    vault.reveal('login', trustedCtx({ sameOriginFrame: false })),
    (e: unknown) => e instanceof CredentialAccessDenied && e.reason === 'cross-origin-frame',
  );
});

test('无用户手势 / 非安全上下文 / 策略关闭均被拒绝', async () => {
  const vault = await CredentialVault.create();
  await vault.store('login', SECRET);
  await assert.rejects(
    vault.reveal('login', trustedCtx({ userActivation: false })),
    (e: unknown) => (e as CredentialAccessDenied).reason === 'no-user-gesture',
  );
  await assert.rejects(
    vault.reveal('login', trustedCtx({ secureContext: false })),
    (e: unknown) => (e as CredentialAccessDenied).reason === 'insecure-context',
  );
  await assert.rejects(
    vault.reveal('login', trustedCtx({ policyAllows: false })),
    (e: unknown) => (e as CredentialAccessDenied).reason === 'permissions-policy',
  );
});

test('DOM 检查：合法自动填充不进入 DOM 可读属性', async () => {
  const vault = await CredentialVault.create();
  await vault.store('login', SECRET);
  const input = new MockInput();
  input.setAttribute('type', 'password');

  await vault.autofill('login', inputSink(input), trustedCtx());

  // 填充生效（IDL 属性内部值已写入）
  assert.equal(input.value, SECRET);
  // 但 DOM content attribute 与序列化结果均不泄露明文
  assert.equal(input.getAttribute('value'), null);
  assert.ok(!input.outerHTML.includes(SECRET));
  assert.ok(!JSON.stringify(input).includes(SECRET));
});

test('策略验证：同一手势限读一次，窗口内限流，凭据不可批量读取', async () => {
  const vault = await CredentialVault.create();
  for (let i = 0; i < 10; i++) await vault.store(`cred-${i}`, `secret-${i}`);

  // 攻击者劫持一次手势，试图循环导出全部凭据
  const hijackedGesture = trustedCtx({ gestureId: 'hijacked-1' });
  const loot: string[] = [];
  for (let i = 0; i < 10; i++) {
    try {
      loot.push(await vault.reveal(`cred-${i}`, hijackedGesture));
    } catch (e) {
      assert.ok(e instanceof CredentialAccessDenied);
      assert.equal(e.reason, 'rate-limit-gesture');
    }
  }
  assert.ok(loot.length <= DEFAULT_POLICY.maxRevealsPerGesture);

  // 攻击者伪造多个手势 id，仍受滑动窗口总量限制
  let windowLoot = 0;
  for (let i = 0; i < 20; i++) {
    try {
      await vault.reveal(`cred-${i % 10}`, trustedCtx());
      windowLoot++;
    } catch (e) {
      assert.equal((e as CredentialAccessDenied).reason, 'rate-limit-window');
    }
  }
  assert.ok(windowLoot <= DEFAULT_POLICY.maxRevealsPerWindow);

  // 组件不暴露任何批量读取 / 枚举明文的 API
  const anyVault = vault as unknown as Record<string, unknown>;
  assert.equal(typeof anyVault.revealAll, 'undefined');
  assert.equal(typeof anyVault.export, 'undefined');
  assert.ok(!JSON.stringify(vault).includes('secret-'));

  // 所有拒绝均进入审计日志
  const denies = vault.audit.entries().filter((e) => e.action === 'deny');
  assert.ok(denies.length >= 9);
});

test('审计：哈希链完整，篡改可检测', async () => {
  const vault = await CredentialVault.create();
  await vault.store('login', SECRET);
  await vault.reveal('login', trustedCtx());
  assert.equal(await vault.audit.verify(), true);

  // 删除/替换历史记录后校验失败（条目本身已冻结，篡改者只能动数组）
  const entries = vault.audit.entries() as unknown as { splice(i: number, n: number): void }[];
  (entries as unknown as unknown[]).splice(0, 1);
  assert.equal(await vault.audit.verify(), false);
});
