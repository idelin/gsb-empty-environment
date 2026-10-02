import test from "node:test";
import assert from "node:assert/strict";
import {
  cellIndexKey,
  deriveKeys,
  generateMasterKey,
  pseudonymFor,
  seal,
  unseal,
} from "../src/crypto.ts";

const keys = deriveKeys(generateMasterKey());

test("时变假名：同一实体不同时间桶产生不同假名", () => {
  const a = pseudonymFor(keys.pseudonymKey, "user-1", 100);
  const b = pseudonymFor(keys.pseudonymKey, "user-1", 101);
  assert.notEqual(a, b);
});

test("时变假名：相同输入确定性输出，不同实体互不相同", () => {
  const a1 = pseudonymFor(keys.pseudonymKey, "user-1", 100);
  const a2 = pseudonymFor(keys.pseudonymKey, "user-1", 100);
  const other = pseudonymFor(keys.pseudonymKey, "user-2", 100);
  assert.equal(a1, a2);
  assert.notEqual(a1, other);
});

test("假名与网格索引键不泄露明文结构", () => {
  const p = pseudonymFor(keys.pseudonymKey, "user-1", 100);
  const c = cellIndexKey(keys.gridSalt, "31.230:121.474", 100);
  assert.match(p, /^[0-9a-f]{32}$/);
  assert.match(c, /^[0-9a-f]{32}$/);
  assert.ok(!p.includes("user-1"));
  assert.ok(!c.includes("31.230"));
});

test("加盐网格哈希：相邻网格同一桶索引键不同，同格跨桶也不同", () => {
  const k1 = cellIndexKey(keys.gridSalt, "31.230:121.474", 100);
  const k2 = cellIndexKey(keys.gridSalt, "31.231:121.474", 100);
  const k3 = cellIndexKey(keys.gridSalt, "31.230:121.474", 101);
  assert.notEqual(k1, k2);
  assert.notEqual(k1, k3);
});

test("记录密封：AES-256-GCM 往返一致", () => {
  const sealed = seal(keys.recordKey, "secret-payload", "aad-1");
  assert.equal(unseal(keys.recordKey, sealed, "aad-1"), "secret-payload");
});

test("记录密封：AAD 不匹配或密文被篡改时拒绝解密", () => {
  const sealed = seal(keys.recordKey, "secret-payload", "aad-1");
  assert.throws(() => unseal(keys.recordKey, sealed, "aad-2"));
  const tampered = { ...sealed, data: sealed.data.slice(0, -4) + "AAAA" };
  assert.throws(() => unseal(keys.recordKey, tampered, "aad-1"));
});

test("密钥派生：同一主密钥派生结果确定，不同主密钥派生不同", () => {
  const master = generateMasterKey();
  const k1 = deriveKeys(master);
  const k2 = deriveKeys(master);
  const k3 = deriveKeys(generateMasterKey());
  assert.deepEqual(k1.pseudonymKey, k2.pseudonymKey);
  assert.notDeepEqual(k1.pseudonymKey, k3.pseudonymKey);
});
