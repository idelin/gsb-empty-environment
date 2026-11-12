import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { test } from "node:test";
import type { SocketConnectOpts } from "node:net";
import {
  ReferralChain,
  ReferralError,
  createReferralSocket,
  isGlobalUnicast,
  parseReferral,
  queryReferral,
  resolveReferral,
  validateQuery,
  type ReferralSocketFactory,
  type ResolvedReferral,
} from "./referral.ts";

const globalV4: ResolvedReferral = {
  hostname: "rr.example.com",
  port: 43,
  address: "8.8.8.8",
  family: 4,
};

class FakeSocket extends EventEmitter {
  endedWith = "";
  private readonly response: string;

  constructor(response: string) {
    super();
    this.response = response;
  }

  setTimeout(): void {}

  end(payload: string): void {
    this.endedWith = payload;
    queueMicrotask(() => {
      this.emit("data", Buffer.from(this.response, "utf8"));
      this.emit("end");
    });
  }

  destroy(): this {
    return this;
  }
}

test("only recognizes referral lines in the first header block", () => {
  const response = [
    "Domain Name: example.com",
    "ReferralServer: whois://rr.example.com:43",
    "",
    "Registrant Notes:",
    "ReferralServer: whois://forged.example.com:43",
    "Whois Server: also-forged.example.com",
  ].join("\r\n");

  assert.deepEqual(parseReferral(response), {
    hostname: "rr.example.com",
    port: 43,
  });
});

test("ignores forged referral lines appearing only in the body", () => {
  const response = [
    "Domain Name: example.com",
    "",
    "Registrant Notes: ReferralServer: whois://forged.example.com:43",
    "Whois Server: forged.example.com",
  ].join("\n");

  assert.equal(parseReferral(response), null);
});

test("accepts both supported header names and bare hostnames", () => {
  assert.deepEqual(parseReferral("Whois Server: whois.example.net\r\n"), {
    hostname: "whois.example.net",
    port: 43,
  });

  assert.deepEqual(
    parseReferral(
      "ReferralServer: whois://RR.EXAMPLE.NET:43\r\rRegistrant Notes: whois://ignored.example",
    ),
    { hostname: "rr.example.net", port: 43 },
  );

  assert.deepEqual(
    parseReferral("Whois Server: rr.example.net:43\n   \nWhois Server: ignored"),
    { hostname: "rr.example.net", port: 43 },
  );
});

test("does not treat another explicit whois port as 43", () => {
  assert.equal(parseReferral("ReferralServer: rr.example.net:0043\n"), null);
  assert.equal(parseReferral("whois://rr.example.net:043\n"), null);
});

test("rejects IP literals and non-whois ports", () => {
  for (const value of [
    "127.0.0.1",
    "whois://127.0.0.1",
    "whois://[::1]",
    "rr.example.com:6379",
    "whois://rr.example.com:6379",
    "https://rr.example.com:43",
    "user@rr.example.com",
    "whois://user@rr.example.com",
    "rr.example.com/path",
    "whois://rr.example.com/path",
  ]) {
    assert.equal(parseReferral(`ReferralServer: ${value}\n`), null, value);
  }
});

test("validates query protocol injection and option prefixes", () => {
  const longestQuery = "a".repeat(255);
  const tooLongQuery = "a".repeat(256);
  for (const query of [
    "example.com\r",
    "example.com\n",
    "example.com\0",
    "-x",
    "é".repeat(128),
    tooLongQuery,
  ]) {
    assert.throws(() => validateQuery(query), (error: unknown) => {
      assert.ok(error instanceof ReferralError);
      assert.equal(error.code, "INVALID_QUERY");
      return true;
    });
  }

  assert.doesNotThrow(() => validateQuery("example.com"));
  assert.doesNotThrow(() => validateQuery(longestQuery));
  assert.throws(() => validateQuery(""));
});

test("requires every DNS result category to contain a global unicast address", async () => {
  assert.equal(isGlobalUnicast("8.8.8.8"), 4);
  assert.equal(isGlobalUnicast("127.0.0.1"), null);
  assert.equal(isGlobalUnicast("10.0.0.1"), null);
  assert.equal(isGlobalUnicast("169.254.1.1"), null);
  assert.equal(isGlobalUnicast("100::1"), null);
  assert.equal(isGlobalUnicast("::1"), null);
  assert.equal(isGlobalUnicast("fc00::1"), null);
  assert.equal(isGlobalUnicast("fe80::1"), null);
  assert.equal(isGlobalUnicast("2001:db8::1"), null);
  assert.equal(isGlobalUnicast("2001::1"), null);
  assert.equal(isGlobalUnicast("2001:1::1"), null);
  assert.equal(isGlobalUnicast("3ffe::1"), null);
  assert.equal(isGlobalUnicast("::ffff:8.8.8.8"), 6);
  assert.equal(isGlobalUnicast("::ffff:127.0.0.1"), null);
  assert.equal(isGlobalUnicast("2606:4700:4700::1111"), 6);

  await assert.rejects(
    resolveReferral(
      { hostname: "rr.example.com", port: 43 },
      async () => [{ address: "127.0.0.1", family: 4 }],
    ),
    (error: unknown) => {
      assert.ok(error instanceof ReferralError);
      assert.equal(error.code, "NO_GLOBAL_UNICAST_ADDRESS");
      return true;
    },
  );

  await assert.rejects(
    resolveReferral(
      { hostname: "rr.example.com", port: 43 },
      async () => [
        { address: "127.0.0.1", family: 4 },
        { address: "8.8.8.8", family: 4 },
      ],
    ),
    (error: unknown) => {
      assert.ok(error instanceof ReferralError);
      assert.equal(error.code, "NO_GLOBAL_UNICAST_ADDRESS");
      return true;
    },
  );

  const resolved = await resolveReferral(
    { hostname: "rr.example.com", port: 43 },
    async () => [{ address: "8.8.8.8", family: 4 }],
  );

  assert.equal(resolved.address, "8.8.8.8");
});

test("connects to the validated address on port 43", () => {
  let captured: SocketConnectOpts | undefined;
  const factory: ReferralSocketFactory = (options) => {
    captured = options;
    return new FakeSocket("") as never;
  };

  createReferralSocket(globalV4, {}, factory);
  assert.deepEqual(captured, {
    host: "8.8.8.8",
    family: 4,
    port: 43,
  });
});

test("rejects a manually constructed non-global referral connection", () => {
  assert.throws(
    () =>
      createReferralSocket({
        hostname: "rr.example.com",
        port: 43,
        address: "127.0.0.1",
        family: 4,
      }),
    (error: unknown) => {
      assert.ok(error instanceof ReferralError);
      assert.equal(error.code, "INVALID_REFERRAL");
      return true;
    },
  );
});

test("sends a sanitized query to the validated referral", async () => {
  const socket = new FakeSocket("referred response");
  const response = await queryReferral(globalV4, "example.com", {
    createSocket: () => socket as never,
  });

  assert.equal(response, "referred response");
  assert.equal(socket.endedWith, "example.com\r\n");
});

test("follows at most three referrals and blocks loops", async () => {
  const lookup = async () => [{ address: "8.8.8.8", family: 4 }];
  const makeSocket = (response: string): ReferralSocketFactory => () =>
    new FakeSocket(response) as never;

  const loopChain = new ReferralChain("first.example");
  const first = await loopChain.follow(
    "ReferralServer: whois://second.example:43\r\n\r\n",
    "example.com",
    {
      lookup,
      createSocket: makeSocket(
        "ReferralServer: whois://first.example:43\r\n\r\n",
      ),
    },
  );

  assert.equal(first?.referral.hostname, "second.example");
  await assert.rejects(
    loopChain.follow(
      "ReferralServer: whois://first.example:43\r\n\r\n",
      "example.com",
      { lookup, createSocket: makeSocket("") },
    ),
    (error: unknown) => {
      assert.ok(error instanceof ReferralError);
      assert.equal(error.code, "REFERRAL_HOST_REVISITED");
      return true;
    },
  );

  const limitedChain = new ReferralChain();
  for (const host of ["one.example", "two.example", "three.example"]) {
    const result = await limitedChain.follow(
      `ReferralServer: whois://${host}:43\n\n`,
      "example.com",
      { lookup, createSocket: makeSocket("") },
    );
    assert.equal(result?.referral.hostname, host);
  }

  assert.equal(limitedChain.referralCount, 3);
  await assert.rejects(
    limitedChain.follow(
      "ReferralServer: whois://four.example:43\n\n",
      "example.com",
      { lookup, createSocket: makeSocket("") },
    ),
    (error: unknown) => {
      assert.ok(error instanceof ReferralError);
      assert.equal(error.code, "REFERRAL_LIMIT");
      return true;
    },
  );
});
