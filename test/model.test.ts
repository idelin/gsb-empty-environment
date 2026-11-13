import assert from "node:assert/strict";
import test from "node:test";
import { bytesToHex, sha256, utf8 } from "../src/bytes.ts";
import { Chain, MODEL_DISCLAIMER, runtimeFor } from "../src/chain.ts";
import { CallError, SecureHost, VulnerableHost } from "../src/host.ts";
import {
  Authority,
  deriveAddress,
  type DeploymentRecord,
  recordAddress,
  recordDigest,
} from "../src/record.ts";
import { defaultFixture, findShortestExploit } from "../src/search.ts";

const CHAIN_ID = 1;
const OTHER_CHAIN_ID = 2;

const deployer = utf8("deployer-00000000001"); // 20 字节
const salt = sha256(utf8("salt-0"));
const initCode = utf8("init-code-v1");
const honestArgs = utf8("constructor-args-honest");
const evilArgs = utf8("constructor-args-evil");

function makeRecord(
  chain: Chain,
  overrides: Partial<DeploymentRecord> = {},
): DeploymentRecord {
  return {
    chainId: chain.chainId,
    deployer,
    salt,
    initCodeDigest: sha256(initCode),
    expectedRuntimeCodeDigest: sha256(runtimeFor(initCode, honestArgs)),
    constructorArgsDigest: sha256(honestArgs),
    generation: chain.generationOf(
      deriveAddress(deployer, salt, sha256(initCode)),
    ),
    ...overrides,
  };
}

function setup(chainId = CHAIN_ID) {
  const chain = new Chain(chainId);
  const authority = new Authority(sha256(utf8("authority-secret")));
  const host = new SecureHost(chain, authority);
  const record = makeRecord(chain);
  const key = host.submitRecord(record);
  const receipt = host.issuePreAuthorization(key);
  const address = recordAddress(record);
  return { chain, authority, host, record, key, receipt, address };
}

function deployHonest(chain: Chain, args = honestArgs) {
  return chain.deploy({ deployer, salt, initCode, constructorArgs: args });
}

test("确定性地址：后20字节 SHA256(0xff||deployer20||salt32||SHA256(initCode))", () => {
  const digest = sha256(initCode);
  const address = deriveAddress(deployer, salt, digest);
  assert.equal(address.length, 20);
  const manual = sha256(
    Uint8Array.of(0xff),
    deployer,
    salt,
    digest,
  ).slice(12);
  assert.equal(bytesToHex(address), bytesToHex(manual));
  // 确定性
  assert.equal(
    bytesToHex(deriveAddress(deployer, salt, digest)),
    bytesToHex(address),
  );
});

test("定长字段校验：deployer/salt/摘要长度不符即拒绝", () => {
  assert.throws(() => deriveAddress(utf8("short"), salt, sha256(initCode)));
  assert.throws(() => deriveAddress(deployer, utf8("short"), sha256(initCode)));
  assert.throws(() => deriveAddress(deployer, salt, utf8("short")));
  const chain = new Chain(CHAIN_ID);
  const host = new SecureHost(chain, new Authority(sha256(utf8("secret-0001"))));
  const record = makeRecord(chain);
  assert.throws(() =>
    host.submitRecord({ ...record, deployer: utf8("short") }),
  );
});

test("salt 相同但 initCode 不同 → 地址不同", () => {
  const a1 = deriveAddress(deployer, salt, sha256(utf8("init-A")));
  const a2 = deriveAddress(deployer, salt, sha256(utf8("init-B")));
  assert.notEqual(bytesToHex(a1), bytesToHex(a2));
});

test("构造参数遗漏：记录必须绑定构造参数摘要，且不同参数产生不同 runtimeCode", () => {
  const chain = new Chain(CHAIN_ID);
  const host = new SecureHost(chain, new Authority(sha256(utf8("secret-0002"))));
  const record = makeRecord(chain);
  // 遗漏构造参数摘要（长度为空）→ 记录不合法
  assert.throws(() =>
    host.submitRecord({
      ...record,
      constructorArgsDigest: new Uint8Array(0),
    }),
  );
  // 同一 initCode 不同构造参数 → 同地址但 runtimeCode 不同
  const honest = runtimeFor(initCode, honestArgs);
  const evil = runtimeFor(initCode, evilArgs);
  assert.notEqual(bytesToHex(honest), bytesToHex(evil));
  // 攻击者用 evilArgs 部署：runtime 摘要不匹配，不能激活
  const key = host.submitRecord(record);
  const receipt = host.issuePreAuthorization(key);
  const evilCallback = chain.deploy({
    deployer,
    salt,
    initCode,
    constructorArgs: evilArgs,
  });
  assert.equal(host.onDeployed(evilCallback, receipt), false);
  assert.equal(host.isActive(key), false);
});

test("完整流程：记录+预授权+部署回调全匹配才激活，Call 成功", () => {
  const { chain, host, key, receipt } = setup();
  const callback = deployHonest(chain);
  assert.equal(callback.success, true);
  assert.ok(callback.txId > 0 && callback.blockHeight > 0);
  assert.equal(host.onDeployed(callback, receipt), true);
  assert.equal(host.isActive(key), true);
  const result = host.call(receipt);
  assert.match(result, /^sensitive-callback@/);
  assert.equal(host.sensitiveLog.length, 1);
});

test("预授权只能针对完整记录签发，不能只存地址", () => {
  const { host } = setup();
  assert.throws(
    () => host.issuePreAuthorization(bytesToHex(sha256(utf8("not-a-record")))),
    /complete deployment record/,
  );
});

test("跨 chainID：回执与记录都不可复制到另一条链", () => {
  const { receipt, record } = setup(CHAIN_ID);
  const otherChain = new Chain(OTHER_CHAIN_ID);
  const authority = new Authority(sha256(utf8("authority-secret")));
  const otherHost = new SecureHost(otherChain, authority);
  // 记录绑定 chainID=1，在 chainID=2 的宿主上登记即被拒绝
  assert.throws(() => otherHost.submitRecord(record), /chainId/);
  // 手工把记录塞进另一条链的宿主也无法通过 chainId 核对
  const forged = { ...receipt };
  const callback = otherChain.deploy({
    deployer,
    salt,
    initCode,
    constructorArgs: honestArgs,
  });
  assert.equal(otherHost.onDeployed(callback, forged), false);
  assert.throws(() => otherHost.call(forged), CallError);
});

test("部署失败后重试：失败不激活，重试成功才激活", () => {
  const { chain, host, key, receipt } = setup();
  const failed = chain.deploy(
    { deployer, salt, initCode, constructorArgs: honestArgs },
    { revert: true },
  );
  assert.equal(failed.success, false);
  assert.equal(host.onDeployed(failed, receipt), false);
  assert.equal(host.isActive(key), false);
  assert.throws(() => host.call(receipt), CallError);
  // 相同参数重试成功
  const retried = deployHonest(chain);
  assert.equal(retried.success, true);
  assert.equal(host.onDeployed(retried, receipt), true);
  assert.doesNotThrow(() => host.call(receipt));
});

test("代码为空：空 runtimeCode 不能激活，也不能调用", () => {
  const chain = new Chain(CHAIN_ID);
  const authority = new Authority(sha256(utf8("authority-secret")));
  const host = new SecureHost(chain, authority);
  const emptyInit = new Uint8Array(0);
  const record: DeploymentRecord = {
    chainId: CHAIN_ID,
    deployer,
    salt,
    initCodeDigest: sha256(emptyInit),
    expectedRuntimeCodeDigest: sha256(new Uint8Array(0)),
    constructorArgsDigest: sha256(honestArgs),
    generation: 0,
  };
  const key = host.submitRecord(record);
  const receipt = host.issuePreAuthorization(key);
  const callback = chain.deploy({
    deployer,
    salt,
    initCode: emptyInit,
    constructorArgs: honestArgs,
  });
  assert.equal(callback.success, true);
  assert.equal(callback.runtimeCode?.length, 0);
  assert.equal(chain.codeOf(callback.address), null);
  assert.equal(host.onDeployed(callback, receipt), false);
  assert.throws(() => host.call(receipt), CallError);
});

test("Destroy：代码清空、代次递增，旧授权永久失效，新代次需新记录新授权", () => {
  const { chain, host, key, receipt, address } = setup();
  assert.equal(host.onDeployed(deployHonest(chain), receipt), true);
  assert.equal(chain.generationOf(address), 0);

  chain.destroy(address);
  host.onDestroyed(address);
  assert.equal(chain.codeOf(address), null);
  assert.equal(chain.generationOf(address), 1);

  // 旧授权永久失效：即使同地址同代码在新代次重建
  const rebuilt = deployHonest(chain);
  assert.equal(rebuilt.generation, 1);
  assert.equal(host.onDeployed(rebuilt, receipt), false);
  assert.throws(() => host.call(receipt), CallError);
  assert.throws(() => host.issuePreAuthorization(key), /invalidated/);

  // 新代次必须新记录 + 新授权
  const record2 = makeRecord(chain);
  assert.equal(record2.generation, 1);
  const key2 = host.submitRecord(record2);
  assert.notEqual(key2, key);
  const receipt2 = host.issuePreAuthorization(key2);
  // 代码未变化（已占用）→ 先 Destroy 再按新记录重新部署
  chain.destroy(address);
  host.onDestroyed(address);
  const record3 = makeRecord(chain);
  const key3 = host.submitRecord(record3);
  const receipt3 = host.issuePreAuthorization(key3);
  assert.equal(host.onDeployed(deployHonest(chain), receipt3), true);
  assert.doesNotThrow(() => host.call(receipt3));
  // 上一代次的授权同样已失效
  assert.throws(() => host.call(receipt2), CallError);
});

test("同一代次代码不可变：地址被占用时禁止再次 Deploy", () => {
  const { chain } = setup();
  deployHonest(chain);
  assert.throws(() => deployHonest(chain), /occupied/);
});

test("执行中的代码变化使调用失败", () => {
  const { chain, host, receipt, address } = setup();
  host.onDeployed(deployHonest(chain), receipt);
  assert.throws(
    () =>
      host.call(receipt, () => {
        // 被调代码在执行期间自毁（重入）
        chain.destroy(address);
        host.onDestroyed(address);
      }),
    /code changed during execution/,
  );
  assert.equal(host.sensitiveLog.length, 0);
});

test("每次 Call 前都核对当前代码摘要与代次（无缓存放行）", () => {
  const { chain, host, receipt, address } = setup();
  host.onDeployed(deployHonest(chain), receipt);
  assert.doesNotThrow(() => host.call(receipt));
  // 链上状态被外部改变（Destroy）后，旧授权立即失效
  chain.destroy(address);
  host.onDestroyed(address);
  assert.throws(() => host.call(receipt), CallError);
});

test("攻击搜索：待审实现的最短序列为 Deploy/Destroy/Deploy/Call", () => {
  const trace = findShortestExploit();
  assert.deepEqual(trace, ["deploy-honest", "destroy", "deploy-evil", "call"]);
  // 深度 3 内不存在攻击序列
  assert.equal(findShortestExploit(defaultFixture(), 3), null);
});

test("同一攻击序列在安全宿主上无法执行敏感回调", () => {
  const { chain, host, receipt, address } = setup();
  // deploy-honest：正常激活
  assert.equal(host.onDeployed(deployHonest(chain), receipt), true);
  // destroy
  chain.destroy(address);
  host.onDestroyed(address);
  // deploy-evil：同地址（同 deployer/salt/initCode）不同构造参数 → 不同 runtimeCode
  const evilCallback = chain.deploy({
    deployer,
    salt,
    initCode,
    constructorArgs: evilArgs,
  });
  assert.equal(host.onDeployed(evilCallback, receipt), false);
  // call：旧授权已永久失效，敏感回调不会执行
  assert.throws(() => host.call(receipt), CallError);
  assert.equal(host.sensitiveLog.length, 0);
  // 而待审实现在同样序列下会放行
  const vulnerableChain = new Chain(CHAIN_ID);
  const vulnerable = new VulnerableHost(vulnerableChain);
  const fixtureAddress = deriveAddress(deployer, salt, sha256(initCode));
  vulnerable.preAuthorize(
    fixtureAddress,
    sha256(runtimeFor(initCode, honestArgs)),
  );
  vulnerable.onDeployed(
    vulnerableChain.deploy({ deployer, salt, initCode, constructorArgs: honestArgs }),
  );
  vulnerableChain.destroy(fixtureAddress);
  vulnerableChain.deploy({ deployer, salt, initCode, constructorArgs: evilArgs });
  assert.doesNotThrow(() => vulnerable.call(fixtureAddress));
  assert.equal(vulnerable.sensitiveLog.length, 1);
});

test("免责声明：模型不声称复刻特定链当前自毁规则", () => {
  assert.match(MODEL_DISCLAIMER, /不声称复刻/);
  assert.match(MODEL_DISCLAIMER, /自毁/);
});
