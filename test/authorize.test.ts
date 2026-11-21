import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  authorizeAndDispatch,
  MethodError,
  RequestError,
  type MethodCall,
  type MethodDescriptor,
  type MethodResponse,
  type Session,
} from "../src/index.ts";

const MAIL = "urn:ietf:params:jmap:mail";
const SUBMISSION = "urn:ietf:params:jmap:submission";
const CONTACTS = "urn:ietf:params:jmap:contacts";

const ALICE = "alice-account";
const BOB = "bob-account";
const READONLY = "readonly-account";
const MAIL_ONLY = "mail-only-account";

function buildSession(): Session {
  return {
    name: "alice-session",
    accounts: new Map([
      [ALICE, {
        name: "alice",
        capabilities: new Set([MAIL, SUBMISSION, CONTACTS]),
        isReadOnly: false,
      }],
      [READONLY, {
        name: "readonly",
        capabilities: new Set([MAIL]),
        isReadOnly: true,
      }],
      [MAIL_ONLY, {
        name: "mail-only",
        capabilities: new Set([MAIL]),
        isReadOnly: false,
      }],
    ]),
    // bob 不在会话 accounts 中：代表属于他人的账户。
    capabilities: new Set([MAIL, SUBMISSION, CONTACTS]),
    maxCallsInRequest: 4,
  };
}

function buildMethods(): Map<string, MethodDescriptor> {
  return new Map<string, MethodDescriptor>([
    ["Email/query", {
      capability: MAIL,
      access: "read",
      handler: (args) => ({
        accountId: args.accountId,
        ids: ["msg-1", "msg-2"],
        queryState: "0",
      }),
    }],
    ["Email/set", {
      capability: MAIL,
      access: "write",
      handler: (args) => ({ accountId: args.accountId, oldState: "1", newState: "2" }),
    }],
    ["EmailSubmission/set", {
      capability: SUBMISSION,
      access: "write",
      handler: (args) => ({ accountId: args.accountId, oldState: "1", newState: "2" }),
    }],
    ["Contact/query", {
      capability: CONTACTS,
      access: "read",
      handler: (args) => ({
        accountId: args.accountId,
        ids: ["contact-1"],
        queryState: "0",
      }),
    }],
    ["Throws/always", {
      capability: MAIL,
      access: "read",
      handler: () => {
        throw new MethodError("serverFail", "处理器主动报错");
      },
    }],
  ]);
}

function dispatch(
  methodCalls: readonly MethodCall[],
  using: readonly string[] = [MAIL, SUBMISSION, CONTACTS],
  session: Session = buildSession(),
): MethodResponse[] {
  return authorizeAndDispatch({
    session,
    using,
    methodCalls,
    methods: buildMethods(),
  });
}

function expectError(
  response: MethodResponse,
  callId: string,
  errorType: string,
): Record<string, unknown> {
  assert.equal(response[0], "error");
  assert.equal(response[2], callId);
  assert.equal(response[1].type, errorType);
  return response[1];
}

describe("账户授权：混入他人账户", () => {
  it("对不在会话中的 accountId 返回 forbidden，且后续调用继续执行", () => {
    const responses = dispatch([
      ["Email/query", { accountId: BOB }, "c1"],
      ["Email/query", { accountId: ALICE }, "c2"],
    ]);

    assert.equal(responses.length, 2);
    const details = expectError(responses[0], "c1", "forbidden");
    assert.equal(details.accountId, BOB);
    assert.equal(details.reason, "accountNotInSession");

    assert.deepEqual(responses[1], [
      "Email/query",
      { accountId: ALICE, ids: ["msg-1", "msg-2"], queryState: "0" },
      "c2",
    ]);
  });

  it("账户缺少方法所需能力时该调用 forbidden", () => {
    const responses = dispatch([
      ["EmailSubmission/set", { accountId: MAIL_ONLY }, "c1"],
    ]);
    expectError(responses[0], "c1", "forbidden");
    assert.equal(responses[0][1].reason, "accountMissingCapability");
  });

  it("只读账户上的写方法 forbidden，读方法放行", () => {
    const responses = dispatch([
      ["Email/set", { accountId: READONLY, create: {} }, "write"],
      ["Email/query", { accountId: READONLY }, "read"],
    ]);

    expectError(responses[0], "write", "forbidden");
    assert.equal(responses[0][1].reason, "readOnlyAccount");
    assert.equal(responses[1][0], "Email/query");
  });
});

describe("能力授权：using 未声明", () => {
  it("方法所属能力未在 using 声明时该调用 unknownCapability，后续调用继续", () => {
    const responses = dispatch(
      [
        ["EmailSubmission/set", { accountId: ALICE }, "submission"],
        ["Email/query", { accountId: ALICE }, "mail"],
      ],
      [MAIL],
    );

    expectError(responses[0], "submission", "unknownCapability");
    assert.equal(responses[0][1].requiredCapability, SUBMISSION);
    assert.equal(responses[1][0], "Email/query");
  });

  it("using 声明服务端不支持的能力时整个请求 unknownCapability", () => {
    const bogus = "urn:ietf:params:jmap:calendars";
    assert.throws(
      () => dispatch([["Email/query", { accountId: ALICE }, "c1"]], [bogus]),
      (error: unknown) => {
        assert.ok(error instanceof RequestError);
        assert.equal(error.type, "unknownCapability");
        assert.equal(error.details.capability, bogus);
        return true;
      },
    );
  });

  it("未注册方法返回 unknownMethod", () => {
    const responses = dispatch([
      ["Ninja/vanish", { accountId: ALICE }, "c1"],
    ]);
    expectError(responses[0], "c1", "unknownMethod");
  });
});

describe("调用数限制", () => {
  it("超过 maxCallsInRequest 时整个请求以 limit 拒绝，不执行任何调用", () => {
    const calls: MethodCall[] = [
      ["Email/query", { accountId: ALICE }, "c1"],
      ["Email/query", { accountId: ALICE }, "c2"],
      ["Email/query", { accountId: ALICE }, "c3"],
      ["Email/query", { accountId: ALICE }, "c4"],
      ["Email/query", { accountId: ALICE }, "c5"],
    ];

    assert.throws(
      () => dispatch(calls),
      (error: unknown) => {
        assert.ok(error instanceof RequestError);
        assert.equal(error.type, "limit");
        assert.equal(error.details.limit, "maxCallsInRequest");
        assert.equal(error.details.received, 5);
        return true;
      },
    );
  });

  it("恰好等于上限时正常处理", () => {
    const calls: MethodCall[] = [
      ["Email/query", { accountId: ALICE }, "c1"],
      ["Email/query", { accountId: ALICE }, "c2"],
      ["Email/query", { accountId: ALICE }, "c3"],
      ["Email/query", { accountId: ALICE }, "c4"],
    ];
    assert.equal(dispatch(calls).length, 4);
  });
});

describe("结果引用：指向失败调用", () => {
  it("引用返回错误的调用时，当前调用 invalidResultReference", () => {
    const responses = dispatch([
      ["Throws/always", { accountId: ALICE }, "failed"],
      ["Email/query", {
        accountId: ALICE,
        ids: {
          resultOf: "failed",
          name: "Throws/always",
          path: "/ids",
        },
      }, "consumer"],
    ]);

    expectError(responses[0], "failed", "serverFail");
    expectError(responses[1], "consumer", "invalidResultReference");
  });

  it("引用不存在或尚未执行的调用 id 时 invalidResultReference", () => {
    const responses = dispatch([
      ["Email/query", {
        accountId: ALICE,
        ids: { resultOf: "ghost", name: "Email/query", path: "/ids" },
      }, "c1"],
    ]);
    expectError(responses[0], "c1", "invalidResultReference");
  });

  it("引用方法名不匹配或路径不可解析时 invalidResultReference", () => {
    const wrongName = dispatch([
      ["Email/query", { accountId: ALICE }, "src"],
      ["Email/query", {
        accountId: ALICE,
        ids: { resultOf: "src", name: "Email/set", path: "/ids" },
      }, "c1"],
    ]);
    expectError(wrongName[1], "c1", "invalidResultReference");

    const badPath = dispatch([
      ["Email/query", { accountId: ALICE }, "src"],
      ["Email/query", {
        accountId: ALICE,
        ids: { resultOf: "src", name: "Email/query", path: "/nope/0" },
      }, "c2"],
    ]);
    expectError(badPath[1], "c2", "invalidResultReference");
  });

  it("同账户成功引用正常解析，支持数组下标与 * 通配", () => {
    const responses = dispatch([
      ["Email/query", { accountId: ALICE }, "src"],
      ["Email/set", {
        accountId: ALICE,
        firstId: { resultOf: "src", name: "Email/query", path: "/ids/0" },
        allIds: { resultOf: "src", name: "Email/query", path: "/ids/*" },
      }, "consumer"],
    ]);

    assert.equal(responses[0][0], "Email/query");
    assert.equal(responses[1][0], "Email/set");
  });
});

describe("结果引用：跨账户重新授权", () => {
  it("同会话内跨账户引用按被调用方 accountId 重新授权并 forbidden", () => {
    // 两个账户都属于当前会话且授权通过，但引用数据不得跨账户流动。
    const responses = dispatch([
      ["Email/query", { accountId: ALICE }, "alice-call"],
      ["Email/set", {
        accountId: MAIL_ONLY,
        create: {
          "#id": {
            resultOf: "alice-call",
            name: "Email/query",
            path: "/ids/0",
          },
        },
      }, "cross-account"],
    ]);

    assert.equal(responses[0][0], "Email/query");
    const details = expectError(responses[1], "cross-account", "forbidden");
    assert.equal(details.reason, "crossAccountResultReference");
    assert.equal(details.originAccountId, ALICE);
    assert.equal(details.accountId, MAIL_ONLY);
  });

  it("引用接进他人账户调用时被账户校验与引用重授权双重拦截", () => {
    // 攻击者先在自己的账户上拿到数据，再把引用接进对他人账户的调用。
    // BOB 不属于当前会话，账户校验先返回 forbidden，引用数据同样不会被执行。
    const responses = dispatch([
      ["Email/query", { accountId: ALICE }, "alice-call"],
      ["Email/set", {
        accountId: BOB,
        create: {
          "#id": {
            resultOf: "alice-call",
            name: "Email/query",
            path: "/ids/0",
          },
        },
      }, "cross-account"],
    ]);

    assert.equal(responses[0][0], "Email/query");
    const details = expectError(responses[1], "cross-account", "forbidden");
    assert.equal(details.reason, "accountNotInSession");
  });

  it("跨账户引用被拒绝后登记为失败，后续引用它同样失败，无关调用继续", () => {
    const responses = dispatch([
      ["Email/query", { accountId: ALICE }, "a1"],
      ["Email/set", {
        accountId: BOB,
        ids: { resultOf: "a1", name: "Email/query", path: "/ids" },
      }, "cross"],
      ["Email/query", {
        accountId: ALICE,
        ids: { resultOf: "cross", name: "Email/set", path: "/ids" },
      }, "chain"],
      ["Contact/query", { accountId: ALICE }, "clean"],
    ]);

    expectError(responses[1], "cross", "forbidden");
    expectError(responses[2], "chain", "invalidResultReference");
    assert.equal(responses[3][0], "Contact/query");
  });

  it("同会话内两个自有账户之间的引用仍按被调用方账户重新授权并拒绝", () => {
    // 两个账户都属于当前会话，但数据不得经引用跨账户流动。
    const responses = dispatch([
      ["Email/query", { accountId: ALICE }, "src"],
      ["Email/query", {
        accountId: READONLY,
        ids: { resultOf: "src", name: "Email/query", path: "/ids" },
      }, "dst"],
    ]);

    expectError(responses[1], "dst", "forbidden");
    assert.equal(responses[1][1].reason, "crossAccountResultReference");
  });
});
