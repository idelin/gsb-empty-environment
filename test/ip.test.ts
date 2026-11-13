import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { isPublicIp, parseIpv4, parseIpv6, parseIpLiteral } from "../src/ip.ts";

describe("parseIpv4 宽松字面量", () => {
  it("解析常规点分十进制", () => {
    assert.deepEqual(parseIpv4("192.168.1.1")?.bytes, [192, 168, 1, 1]);
  });

  it("解析十六进制写法 0x7f.1", () => {
    assert.deepEqual(parseIpv4("0x7f.1")?.bytes, [127, 0, 0, 1]);
    assert.deepEqual(parseIpv4("0x7f.0.0.1")?.bytes, [127, 0, 0, 1]);
  });

  it("解析八进制写法", () => {
    assert.deepEqual(parseIpv4("0177.0.0.1")?.bytes, [127, 0, 0, 1]);
  });

  it("解析单段十进制整数 2130706433", () => {
    assert.deepEqual(parseIpv4("2130706433")?.bytes, [127, 0, 0, 1]);
    assert.deepEqual(parseIpv4("0x7f000001")?.bytes, [127, 0, 0, 1]);
  });

  it("解析 2~3 段压缩写法", () => {
    assert.deepEqual(parseIpv4("127.1")?.bytes, [127, 0, 0, 1]);
    assert.deepEqual(parseIpv4("192.168.257")?.bytes, [192, 168, 1, 1]);
  });

  it("拒绝越界与非法输入", () => {
    assert.equal(parseIpv4("256.0.0.1"), undefined);
    assert.equal(parseIpv4("4294967296"), undefined);
    assert.equal(parseIpv4("1.2.3.4.5"), undefined);
    assert.equal(parseIpv4("example.com"), undefined);
    assert.equal(parseIpv4("not-an-ip-1"), undefined);
  });
});

describe("parseIpv6", () => {
  it("支持 :: 压缩与内嵌 IPv4", () => {
    assert.deepEqual(parseIpv6("::1")?.bytes.slice(15), [1]);
    assert.equal(parseIpv6("fe80::1")?.bytes[0], 0xfe);
    const mapped = parseIpv6("::ffff:169.254.169.254");
    assert.deepEqual(mapped?.bytes.slice(12), [169, 254, 169, 254]);
  });

  it("拒绝多个 :: 与非法分组", () => {
    assert.equal(parseIpv6("::1::2"), undefined);
    assert.equal(parseIpv6("gggg::1"), undefined);
    assert.equal(parseIpv6("2001:db8:0:0:0:0:0:0:1"), undefined);
  });
});

describe("isPublicIp", () => {
  const cases: Array<[string, boolean]> = [
    ["8.8.8.8", true],
    ["1.1.1.1", true],
    ["127.0.0.1", false],
    ["0x7f.1", false],
    ["2130706433", false],
    ["10.0.0.5", false],
    ["172.16.0.1", false],
    ["172.31.255.255", false],
    ["172.32.0.1", true],
    ["192.168.10.10", false],
    ["169.254.169.254", false],
    ["100.64.0.1", false],
    ["0.0.0.0", false],
    ["224.0.0.1", false],
    ["240.0.0.1", false],
    ["192.0.2.9", false],
    ["198.51.100.7", false],
    ["203.0.113.44", false],
    ["2606:4700:4700::1111", true],
    ["::1", false],
    ["fe80::1", false],
    ["fc00::1", false],
    ["fd12::1", false],
    ["ff02::1", false],
    ["2001:db8::1", false],
    ["::ffff:169.254.169.254", false],
    ["::ffff:8.8.8.8", true],
  ];

  for (const [literal, expected] of cases) {
    it(`${literal} 公网判定为 ${expected}`, () => {
      const parsed = parseIpLiteral(literal);
      assert.ok(parsed, `应能解析 ${literal}`);
      assert.equal(isPublicIp(parsed!), expected);
    });
  }
});
