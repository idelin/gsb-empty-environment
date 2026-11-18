import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createHash,
  generateKeyPairSync,
  privateDecrypt,
  constants,
} from "node:crypto";
import { evaluatePolicy, type Claims, type Policy } from "./policy.ts";
import { releaseKey } from "./skr.ts";

const { publicKey: vmPublicKey, privateKey: vmPrivateKey } =
  generateKeyPairSync("rsa", { modulusLength: 2048 });
const { publicKey: attackerPublicKey } = generateKeyPairSync("rsa", {
  modulusLength: 2048,
});

const vmPublicKeyPem = publicKeyToPem();
const SECRET_KEY = Buffer.from("0123456789abcdef0123456789abcdef");

function publicKeyToPem(): string {
  return vmPublicKey.export({ type: "spki", format: "pem" }).toString();
}

function makeRuntimeData(publicKeyPem: string): string {
  return Buffer.from(JSON.stringify({ publicKey: publicKeyPem })).toString(
    "base64",
  );
}

function reportDataOf(runtimeDataB64: string): string {
  return createHash("sha256")
    .update(Buffer.from(runtimeDataB64, "base64"))
    .digest("hex");
}

/** 构造一份"正常"令牌声明:非调试、合规、TCB 达标、runtime-data 与 REPORT_DATA 绑定。 */
function goodClaims(): Claims {
  const runtimeData = makeRuntimeData(vmPublicKeyPem);
  return {
    "x-ms-isolation-tee": {
      "x-ms-attestation-type": "sevsnpvm",
      "x-ms-compliance-status": "azure-compliant-cvm",
      "x-ms-sevsnpvm-is-debuggable": false,
      "x-ms-sevsnpvm-tcb-version": [3, 0, 0, 0, 0, 0, 0, 8],
    },
    "runtime-data": runtimeData,
    "report-data": reportDataOf(runtimeData),
  };
}

const POLICY: Policy = {
  allOf: [
    {
      claim: "x-ms-isolation-tee.x-ms-attestation-type",
      equals: "sevsnpvm",
    },
    { claim: "x-ms-isolation-tee.x-ms-sevsnpvm-is-debuggable", equals: false },
    {
      claim: "x-ms-isolation-tee.x-ms-sevsnpvm-tcb-version",
      tcbAtLeast: [3, 0, 0, 0, 0, 0, 0, 3],
    },
  ],
};

/** 测试用验签器:模拟调用方注入的验签,token 即 JSON 字符串。 */
function verifyToken(token: string): Claims | null {
  if (token === "garbage") {
    return null;
  }
  return JSON.parse(token) as Claims;
}

function attemptRelease(claims: Claims, policy: Policy = POLICY) {
  return releaseKey(JSON.stringify(claims), SECRET_KEY, {
    policy,
    verifyToken,
  });
}

test("合法令牌:策略通过,密钥用 runtime-data 公钥包裹且可解", () => {
  const result = attemptRelease(goodClaims());
  assert.ok(result.ok, `expected release, got ${JSON.stringify(result)}`);
  const unwrapped = privateDecrypt(
    {
      key: vmPrivateKey,
      padding: constants.RSA_PKCS1_OAEP_PADDING,
      oaepHash: "sha256",
    },
    result.wrappedKey,
  );
  assert.deepEqual(unwrapped, SECRET_KEY);
});

test("验签失败:注入的验签器返回 null 即拒绝", () => {
  const result = releaseKey("garbage", SECRET_KEY, {
    policy: POLICY,
    verifyToken,
  });
  assert.equal(result.ok, false);
});

test("缺失声明:is-debuggable 不存在时不满足", () => {
  const claims = goodClaims();
  const tee = claims["x-ms-isolation-tee"] as Claims;
  delete tee["x-ms-sevsnpvm-is-debuggable"];
  const result = attemptRelease(claims);
  assert.deepEqual(result, { ok: false, reason: "policy not satisfied" });
});

test("类型混淆:字符串 \"false\" 不等于布尔 false", () => {
  const claims = goodClaims();
  (claims["x-ms-isolation-tee"] as Claims)["x-ms-sevsnpvm-is-debuggable"] =
    "false";
  assert.equal(attemptRelease(claims).ok, false);
});

test("类型混淆:字符串 \"sevsnpvm\" 之外的同名真值不放宽匹配", () => {
  const claims = goodClaims();
  (claims["x-ms-isolation-tee"] as Claims)["x-ms-attestation-type"] = 1;
  assert.equal(attemptRelease(claims).ok, false);
});

test("旧 TCB:低于下限的版本被拒绝", () => {
  const claims = goodClaims();
  (claims["x-ms-isolation-tee"] as Claims)["x-ms-sevsnpvm-tcb-version"] = [
    3, 0, 0, 0, 0, 0, 0, 2,
  ];
  assert.equal(attemptRelease(claims).ok, false);
});

test("TCB 边界:等于下限与高于下限都接受", () => {
  const atFloor = goodClaims();
  (atFloor["x-ms-isolation-tee"] as Claims)["x-ms-sevsnpvm-tcb-version"] = [
    3, 0, 0, 0, 0, 0, 0, 3,
  ];
  assert.equal(attemptRelease(atFloor).ok, true);

  const above = goodClaims();
  (above["x-ms-isolation-tee"] as Claims)["x-ms-sevsnpvm-tcb-version"] = [
    3, 0, 0, 0, 0, 0, 0, 9,
  ];
  assert.equal(attemptRelease(above).ok, true);
});

test("TCB 类型混淆:字符串形式的版本号不满足下限", () => {
  const claims = goodClaims();
  (claims["x-ms-isolation-tee"] as Claims)["x-ms-sevsnpvm-tcb-version"] =
    "3.0.0.0.0.0.0.8";
  assert.equal(attemptRelease(claims).ok, false);
});

test("换公钥:runtime-data 被替换后哈希与 REPORT_DATA 不符", () => {
  const claims = goodClaims();
  const attackerPem = attackerPublicKey
    .export({ type: "spki", format: "pem" })
    .toString();
  // 主机把 runtime-data 换成含攻击者公钥的数据,但 REPORT_DATA 仍是原报告的。
  claims["runtime-data"] = makeRuntimeData(attackerPem);
  const result = attemptRelease(claims);
  assert.deepEqual(result, {
    ok: false,
    reason: "runtime-data hash does not match REPORT_DATA",
  });
});

test("嵌套 anyOf:任一分支满足即可,全不满足则拒绝", () => {
  const nestedPolicy: Policy = {
    allOf: [
      { claim: "x-ms-isolation-tee.x-ms-sevsnpvm-is-debuggable", equals: false },
      {
        anyOf: [
          {
            allOf: [
              {
                claim: "x-ms-isolation-tee.x-ms-attestation-type",
                equals: "sevsnpvm",
              },
              {
                claim: "x-ms-isolation-tee.x-ms-compliance-status",
                equals: "azure-compliant-cvm",
              },
            ],
          },
          {
            allOf: [
              {
                claim: "x-ms-isolation-tee.x-ms-attestation-type",
                equals: "tdxvm",
              },
              {
                claim: "x-ms-isolation-tee.x-ms-compliance-status",
                equals: "azure-compliant-cvm",
              },
            ],
          },
        ],
      },
    ],
  };

  // sevsnpvm 分支满足。
  assert.equal(evaluatePolicy(nestedPolicy, goodClaims()), true);
  assert.equal(attemptRelease(goodClaims(), nestedPolicy).ok, true);

  // tdxvm 分支满足。
  const tdxClaims = goodClaims();
  (tdxClaims["x-ms-isolation-tee"] as Claims)["x-ms-attestation-type"] =
    "tdxvm";
  assert.equal(attemptRelease(tdxClaims, nestedPolicy).ok, true);

  // 两个分支都不满足(类型未知)。
  const unknownClaims = goodClaims();
  (unknownClaims["x-ms-isolation-tee"] as Claims)["x-ms-attestation-type"] =
    "sgx";
  assert.equal(attemptRelease(unknownClaims, nestedPolicy).ok, false);

  // 内层 allOf 缺一条件(compliance 缺失)则该分支不满足。
  const missingCompliance = goodClaims();
  delete (missingCompliance["x-ms-isolation-tee"] as Claims)[
    "x-ms-compliance-status"
  ];
  assert.equal(attemptRelease(missingCompliance, nestedPolicy).ok, false);
});
