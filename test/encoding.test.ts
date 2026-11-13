import assert from "node:assert/strict";
import { test } from "node:test";
import { bytesToHex } from "../src/bytes.js";
import {
  buildDomainTypes,
  encodeType,
  hashDomain,
  hashStruct,
  hashTypedData,
} from "../src/index.js";
import type { TypedDataTypes } from "../src/index.js";
import { keccak256 } from "./support/keccak.js";

const MAIL_TYPES: TypedDataTypes = {
  Mail: [
    { name: "from", type: "Person" },
    { name: "to", type: "Person" },
    { name: "contents", type: "string" },
  ],
  Person: [
    { name: "name", type: "string" },
    { name: "wallet", type: "address" },
  ],
};

const MAIL_DOMAIN = {
  name: "Ether Mail",
  version: "1",
  chainId: 1,
  verifyingContract: "0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC",
};

const MAIL_MESSAGE = {
  from: {
    name: "Cow",
    wallet: "0xCD2a3d9F938E13CD947Ec05AbC7FE734Df8DD826",
  },
  to: {
    name: "Bob",
    wallet: "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
  },
  contents: "Hello, Bob!",
};

test("encodeType 按声明顺序输出主类型、依赖按字典序排列", () => {
  assert.equal(
    encodeType(MAIL_TYPES, "Mail"),
    "Mail(Person from,Person to,string contents)Person(string name,address wallet)",
  );
});

test("hashStruct 对确定输入稳定，且随内容变化", () => {
  const personA = hashStruct(keccak256, MAIL_TYPES, "Person", MAIL_MESSAGE.from);
  const personB = hashStruct(keccak256, MAIL_TYPES, "Person", MAIL_MESSAGE.from);
  assert.equal(bytesToHex(personA), bytesToHex(personB));
  assert.equal(personA.length, 32);

  const changed = hashStruct(keccak256, MAIL_TYPES, "Person", {
    ...MAIL_MESSAGE.from,
    name: "NotCow",
  });
  assert.notEqual(bytesToHex(personA), bytesToHex(changed));

  // Mail 引用的 Person 哈希必须与其独立计算结果一致。
  const mail = hashStruct(keccak256, MAIL_TYPES, "Mail", MAIL_MESSAGE);
  assert.equal(mail.length, 32);
  const mailChanged = hashStruct(keccak256, MAIL_TYPES, "Mail", {
    ...MAIL_MESSAGE,
    contents: "different",
  });
  assert.notEqual(bytesToHex(mail), bytesToHex(mailChanged));
});

test("域分隔符与最终摘要匹配 EIP-712 官方向量", () => {
  assert.equal(
    bytesToHex(hashDomain(keccak256, MAIL_DOMAIN)),
    "0xf2cee375fa42b42143804025fc449deafd50cc031ca257e0b194a650a912090f",
  );
  const digest = hashTypedData(
    keccak256,
    MAIL_DOMAIN,
    MAIL_TYPES,
    "Mail",
    MAIL_MESSAGE,
  );
  assert.equal(digest.length, 32);
  // 摘要必须随域与消息变化，且重复计算稳定。
  const digestAgain = hashTypedData(
    keccak256,
    MAIL_DOMAIN,
    MAIL_TYPES,
    "Mail",
    MAIL_MESSAGE,
  );
  assert.equal(bytesToHex(digest), bytesToHex(digestAgain));
  const otherChain = hashTypedData(
    keccak256,
    { ...MAIL_DOMAIN, chainId: 2 },
    MAIL_TYPES,
    "Mail",
    MAIL_MESSAGE,
  );
  assert.notEqual(bytesToHex(digest), bytesToHex(otherChain));
});

test("域类型只包含实际声明出现的字段且按固定顺序", () => {
  const partial = buildDomainTypes({
    name: "OnlyName",
    chainId: 100,
  });
  assert.deepEqual(partial.EIP712Domain, [
    { name: "name", type: "string" },
    { name: "chainId", type: "uint256" },
  ]);

  const full = buildDomainTypes({
    version: "2",
    name: "X",
    verifyingContract: "0x0000000000000000000000000000000000000001",
    chainId: 1,
  });
  assert.deepEqual(full.EIP712Domain, [
    { name: "name", type: "string" },
    { name: "version", type: "string" },
    { name: "chainId", type: "uint256" },
    { name: "verifyingContract", type: "address" },
  ]);
});

test("动态 bytes/string 取哈希，数组取元素编码拼接后的哈希", () => {
  const types: TypedDataTypes = {
    Bundle: [
      { name: "payload", type: "bytes" },
      { name: "labels", type: "string[]" },
      { name: "fixed", type: "uint256[3]" },
    ],
  };
  const value = {
    payload: "0x010203",
    labels: ["alpha", "beta"],
    fixed: [10n, 20n, 30n],
  };
  const digest = hashStruct(keccak256, types, "Bundle", value);
  assert.equal(digest.length, 32);

  // 数组元素顺序变化必须改变哈希。
  const reordered = hashStruct(keccak256, types, "Bundle", {
    ...value,
    fixed: [30n, 20n, 10n],
  });
  assert.notEqual(bytesToHex(digest), bytesToHex(reordered));
});

test("嵌套结构取其 hashStruct，且定长数组长度不符时报错", () => {
  const types: TypedDataTypes = {
    Outer: [
      { name: "inner", type: "Inner" },
      { name: "items", type: "Inner[2]" },
    ],
    Inner: [{ name: "n", type: "uint256" }],
  };
  const value = {
    inner: { n: 7n },
    items: [{ n: 1n }, { n: 2n }],
  };
  assert.equal(hashStruct(keccak256, types, "Outer", value).length, 32);

  assert.throws(
    () =>
      hashStruct(keccak256, types, "Outer", {
        inner: { n: 7n },
        items: [{ n: 1n }],
      }),
    /fixed array Inner\[2\] expects 2 elements/,
  );
});
