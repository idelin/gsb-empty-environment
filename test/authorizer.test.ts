import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  RequestAuthorizer,
  gateRequest,
} from "../src/authorizer.ts";
import { dispatchAuthorized } from "../src/dispatcher.ts";
import { resolvePointer } from "../src/json-pointer.ts";
import type {
  AuthorizationOptions,
  JmapRequest,
  MethodCall,
  MethodResponse,
  Session,
} from "../src/types.ts";

const CORE = "urn:ietf:params:jmap:core";
const MAIL = "urn:ietf:params:jmap:mail";

const ACC_A = "acc-victim-a";
const ACC_B = "acc-attacker-b";
const ACC_READONLY = "acc-readonly";
const ACC_NO_MAIL = "acc-no-mail";

const MB_A1 = "mb-a1";
const MB_A2 = "mb-a2";
const MB_B1 = "mb-b1";
const EM_A1 = "em-a1";
const EM_B1 = "em-b1";

const mailboxOwner: Record<string, string> = {
  [MB_A1]: ACC_A,
  [MB_A2]: ACC_A,
  [MB_B1]: ACC_B,
};
const emailOwner: Record<string, string> = {
  [EM_A1]: ACC_A,
  [EM_B1]: ACC_B,
};

function buildSession(includeVictim = false): Session {
  const accounts: Session["accounts"] = {
    [ACC_B]: {
      name: "attacker@example.com",
      isPersonal: true,
      accountCapabilities: { [CORE]: {}, [MAIL]: {} },
    },
    [ACC_READONLY]: {
      name: "readonly@example.com",
      isPersonal: false,
      isReadOnly: true,
      accountCapabilities: { [CORE]: {}, [MAIL]: {} },
    },
    [ACC_NO_MAIL]: {
      name: "nomail@example.com",
      isPersonal: false,
      accountCapabilities: { [CORE]: {} },
    },
  };
  if (includeVictim) {
    accounts[ACC_A] = {
      name: "victim@example.com",
      isPersonal: true,
      accountCapabilities: { [CORE]: {}, [MAIL]: {} },
    };
  }
  return {
    username: "attacker@example.com",
    apiUrl: "https://example.com/jmap/",
    accounts,
    capabilities: {
      [CORE]: { maxCallsInRequest: 5 },
      [MAIL]: {},
    },
  };
}

function buildOptions(
  session: Session = buildSession(),
): AuthorizationOptions {
  return {
    session,
    methods: {
      "Core/echo": {
        capability: CORE,
        kind: "read",
        requiresAccount: false,
      },
      "Mailbox/query": { capability: MAIL, kind: "read" },
      "Mailbox/get": {
        capability: MAIL,
        kind: "read",
        idBindings: [
          { path: "ids", entityType: "Mailbox", multiple: true },
        ],
      },
      "Mailbox/set": { capability: MAIL, kind: "write" },
      "Email/query": {
        capability: MAIL,
        kind: "read",
        idBindings: [
          { path: "filter.inMailbox", entityType: "Mailbox" },
        ],
      },
      "Email/set": { capability: MAIL, kind: "write" },
    },
    idAccessChecker: ({ accountId, entityType, entityIds }) => {
      const ownerTable =
        entityType === "Mailbox" ? mailboxOwner : emailOwner;
      return entityIds.filter((id) => ownerTable[id] === accountId);
    },
  };
}

function request(
  calls: MethodCall[],
  using: string[] = [CORE, MAIL],
): JmapRequest {
  return { using, methodCalls: calls };
}

describe("请求级预检 gateRequest", () => {
  it("结构错误返回 notRequest", () => {
    const options = buildOptions();
    assert.deepEqual(gateRequest(null, options), {
      ok: false,
      errorType: "notRequest",
      detail: "请求体不是 JSON 对象",
    });
    const missingField = gateRequest({ using: [CORE] }, options);
    assert.equal(missingField.ok, false);
    if (!missingField.ok) {
      assert.equal(missingField.errorType, "notRequest");
    }
  });

  it("using 含服务器不支持的能力时整请求 unknownCapability", () => {
    const gate = gateRequest(
      request([], ["urn:ietf:params:jmap:calendars"]),
      buildOptions(),
    );
    assert.equal(gate.ok, false);
    if (!gate.ok) {
      assert.equal(gate.errorType, "unknownCapability");
    }
  });

  it("调用数超过 maxCallsInRequest 整请求以 limit 拒绝", () => {
    const overLimit: MethodCall[] = Array.from(
      { length: 6 },
      (_, index): MethodCall => [
        "Mailbox/query",
        { accountId: ACC_B },
        `c${index}`,
      ],
    );
    const gate = gateRequest(request(overLimit), buildOptions());
    assert.equal(gate.ok, false);
    if (!gate.ok) {
      assert.equal(gate.errorType, "limit");
      assert.match(gate.detail, /maxCallsInRequest/);
    }

    const atLimit: MethodCall[] = Array.from(
      { length: 5 },
      (_, index): MethodCall => [
        "Mailbox/query",
        { accountId: ACC_B },
        `c${index}`,
      ],
    );
    assert.equal(gateRequest(request(atLimit), buildOptions()).ok, true);
  });
});

describe("场景一：混入他人账户的调用", () => {
  it("他人 accountId 的调用返回 forbidden，且后续调用继续执行", async () => {
    const options = buildOptions();
    const authorizer = RequestAuthorizer.create(
      request([
        ["Mailbox/query", { accountId: ACC_A }, "evil"],
        ["Mailbox/query", { accountId: ACC_B }, "mine"],
      ]),
      options,
    );

    const evil = await authorizer.authorize([
      "Mailbox/query",
      { accountId: ACC_A },
      "evil",
    ]);
    assert.equal(evil.allowed, false);
    if (!evil.allowed) {
      assert.equal(evil.errorType, "forbidden");
      assert.match(evil.detail, /不属于当前会话/);
    }

    const mine = await authorizer.authorize([
      "Mailbox/query",
      { accountId: ACC_B },
      "mine",
    ]);
    assert.equal(mine.allowed, true);
  });

  it("账户不具备方法所需能力时同样 forbidden", async () => {
    const authorizer = RequestAuthorizer.create(
      request([["Mailbox/query", { accountId: ACC_NO_MAIL }, "x"]]),
      buildOptions(),
    );
    const decision = await authorizer.authorize([
      "Mailbox/query",
      { accountId: ACC_NO_MAIL },
      "x",
    ]);
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.errorType, "forbidden");
    }
  });

  it("只读账户上的写方法 forbidden，读方法放行", async () => {
    const authorizer = RequestAuthorizer.create(
      request([
        ["Mailbox/set", { accountId: ACC_READONLY }, "write"],
        ["Mailbox/query", { accountId: ACC_READONLY }, "read"],
      ]),
      buildOptions(),
    );
    const write = await authorizer.authorize([
      "Mailbox/set",
      { accountId: ACC_READONLY },
      "write",
    ]);
    assert.equal(write.allowed, false);
    if (!write.allowed) {
      assert.equal(write.errorType, "forbidden");
      assert.match(write.detail, /只读/);
    }
    const read = await authorizer.authorize([
      "Mailbox/query",
      { accountId: ACC_READONLY },
      "read",
    ]);
    assert.equal(read.allowed, true);
  });
});

describe("场景二：方法所属能力未在 using 声明", () => {
  it("返回 unknownCapability 且只影响该调用", async () => {
    const options = buildOptions();
    const authorizer = RequestAuthorizer.create(
      request(
        [
          ["Mailbox/query", { accountId: ACC_B }, "undeclared"],
          ["Core/echo", { hello: "world" }, "echo"],
        ],
        [CORE],
      ),
      options,
    );

    const undeclared = await authorizer.authorize([
      "Mailbox/query",
      { accountId: ACC_B },
      "undeclared",
    ]);
    assert.equal(undeclared.allowed, false);
    if (!undeclared.allowed) {
      assert.equal(undeclared.errorType, "unknownCapability");
      assert.match(undeclared.detail, /using/);
    }

    const echo = await authorizer.authorize([
      "Core/echo",
      { hello: "world" },
      "echo",
    ]);
    assert.equal(echo.allowed, true);
  });

  it("未登记的方法名返回 unknownMethod", async () => {
    const authorizer = RequestAuthorizer.create(
      request([["Calendar/query", { accountId: ACC_B }, "x"]]),
      buildOptions(),
    );
    const decision = await authorizer.authorize([
      "Calendar/query",
      { accountId: ACC_B },
      "x",
    ]);
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.errorType, "unknownMethod");
    }
  });
});

describe("场景三：结果引用指向失败/不存在的调用", () => {
  it("引用未成功的前置调用返回 invalidResultReference", async () => {
    const authorizer = RequestAuthorizer.create(
      request([
        ["Mailbox/set", { accountId: ACC_READONLY }, "failed"],
        [
          "Email/query",
          {
            accountId: ACC_B,
            filter: {
              inMailbox: {
                resultOf: "failed",
                name: "Mailbox/set",
                path: "/list/0",
              },
            },
          },
          "ref",
        ],
      ]),
      buildOptions(),
    );

    const failed = await authorizer.authorize([
      "Mailbox/set",
      { accountId: ACC_READONLY },
      "failed",
    ]);
    assert.equal(failed.allowed, false);
    // 失败调用不会 recordSuccess，引用因此解析不到。
    const ref = await authorizer.authorize([
      "Email/query",
      {
        accountId: ACC_B,
        filter: {
          inMailbox: {
            resultOf: "failed",
            name: "Mailbox/set",
            path: "/list/0",
          },
        },
      },
      "ref",
    ]);
    assert.equal(ref.allowed, false);
    if (!ref.allowed) {
      assert.equal(ref.errorType, "invalidResultReference");
    }
  });

  it("引用不存在的调用 id、name 不匹配、path 越界均被拒", async () => {
    const options = buildOptions();
    const success: MethodResponse = [
      "Mailbox/query",
      { ids: [MB_B1] },
      "ok",
    ];

    const makeAuthorizer = (): RequestAuthorizer => {
      const authorizer = RequestAuthorizer.create(
        request([
          ["Email/query", { accountId: ACC_B, filter: {} }, "later"],
        ]),
        options,
      );
      authorizer.recordSuccess(success);
      return authorizer;
    };

    const missing = await makeAuthorizer().authorize([
      "Email/query",
      {
        accountId: ACC_B,
        filter: {
          inMailbox: {
            resultOf: "ghost",
            name: "Mailbox/query",
            path: "/ids/0",
          },
        },
      },
      "later",
    ]);
    assert.equal(missing.allowed, false);
    if (!missing.allowed) {
      assert.equal(missing.errorType, "invalidResultReference");
    }

    const nameMismatch = await makeAuthorizer().authorize([
      "Email/query",
      {
        accountId: ACC_B,
        filter: {
          inMailbox: {
            resultOf: "ok",
            name: "Mailbox/get",
            path: "/ids/0",
          },
        },
      },
      "later",
    ]);
    assert.equal(nameMismatch.allowed, false);
    if (!nameMismatch.allowed) {
      assert.equal(nameMismatch.errorType, "invalidResultReference");
    }

    const badPath = await makeAuthorizer().authorize([
      "Email/query",
      {
        accountId: ACC_B,
        filter: {
          inMailbox: {
            resultOf: "ok",
            name: "Mailbox/query",
            path: "/ids/9",
          },
        },
      },
      "later",
    ]);
    assert.equal(badPath.allowed, false);
    if (!badPath.allowed) {
      assert.equal(badPath.errorType, "invalidResultReference");
    }
  });
});

describe("场景四：跨账户结果引用", () => {
  it("引用解出的他人邮箱 id 在被调用方账户上重新授权并 forbidden", async () => {
    const options = buildOptions(buildSession(true));
    const authorizer = RequestAuthorizer.create(
      request([
        ["Mailbox/query", { accountId: ACC_A }, "a-query"],
        [
          "Email/query",
          {
            accountId: ACC_B,
            filter: { inMailbox: null },
          },
          "b-query",
        ],
      ]),
      options,
    );
    // 前置调用本身合法（同会话两个账户），登记其成功输出。
    authorizer.recordSuccess([
      "Mailbox/query",
      { ids: [MB_A1, MB_A2] },
      "a-query",
    ]);

    const cross = await authorizer.authorize([
      "Email/query",
      {
        accountId: ACC_B,
        filter: {
          inMailbox: {
            resultOf: "a-query",
            name: "Mailbox/query",
            path: "/ids/0",
          },
        },
      },
      "b-query",
    ]);
    assert.equal(cross.allowed, false);
    if (!cross.allowed) {
      assert.equal(cross.errorType, "forbidden");
      assert.match(cross.detail, /跨账户引用/);
    }
  });

  it("同账户引用通过二次授权并把引用替换为解出的 id", async () => {
    const options = buildOptions(buildSession(true));
    const authorizer = RequestAuthorizer.create(
      request([
        ["Mailbox/query", { accountId: ACC_B }, "b-query"],
        ["Email/query", { accountId: ACC_B, filter: {} }, "b-email"],
      ]),
      options,
    );
    authorizer.recordSuccess([
      "Mailbox/query",
      { ids: [MB_B1] },
      "b-query",
    ]);

    const decision = await authorizer.authorize([
      "Email/query",
      {
        accountId: ACC_B,
        filter: {
          inMailbox: {
            resultOf: "b-query",
            name: "Mailbox/query",
            path: "/ids/0",
          },
        },
      },
      "b-email",
    ]);
    assert.equal(decision.allowed, true);
    if (decision.allowed) {
      assert.equal(
        (decision.resolvedArgs as { filter: { inMailbox: string } }).filter
          .inMailbox,
        MB_B1,
      );
    }
  });

  it("数组绑定中混入他人 id 时整体 forbidden", async () => {
    const options = buildOptions(buildSession(true));
    const authorizer = RequestAuthorizer.create(
      request([
        ["Mailbox/query", { accountId: ACC_A }, "a-query"],
        ["Mailbox/get", { accountId: ACC_B, ids: [] }, "b-get"],
      ]),
      options,
    );
    authorizer.recordSuccess([
      "Mailbox/query",
      { ids: [MB_B1, MB_A1] },
      "a-query",
    ]);

    const decision = await authorizer.authorize([
      "Mailbox/get",
      {
        accountId: ACC_B,
        ids: {
          resultOf: "a-query",
          name: "Mailbox/query",
          path: "/ids",
        },
      },
      "b-get",
    ]);
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.errorType, "forbidden");
      assert.match(decision.detail, new RegExp(MB_A1));
    }
  });
});

describe("分发器集成 dispatchAuthorized", () => {
  it("单调用被拒只产生 error 响应，后续成功调用照常执行", async () => {
    const executed: string[] = [];
    const outcome = await dispatchAuthorized(
      request([
        ["Mailbox/query", { accountId: ACC_A }, "evil"],
        ["Mailbox/query", { accountId: ACC_B }, "ok"],
      ]),
      buildOptions(),
      async ({ methodName, clientId }) => {
        executed.push(clientId);
        return { methodName, placeholder: true };
      },
    );

    assert.equal(outcome.rejected, false);
    if (!outcome.rejected) {
      assert.deepEqual(executed, ["ok"]);
      assert.equal(outcome.methodResponses.length, 2);
      const errorResponse = outcome.methodResponses[0];
      assert.equal(errorResponse[0], "error");
      assert.equal(
        (errorResponse[1] as { type: string }).type,
        "forbidden",
      );
      assert.equal(errorResponse[2], "evil");
      assert.equal(outcome.methodResponses[1][0], "Mailbox/query");
      assert.equal(outcome.methodResponses[1][2], "ok");
    }
  });

  it("超限时整请求拒绝且不执行任何调用", async () => {
    const calls: MethodCall[] = Array.from(
      { length: 6 },
      (_, index): MethodCall => [
        "Mailbox/query",
        { accountId: ACC_B },
        `c${index}`,
      ],
    );
    const outcome = await dispatchAuthorized(
      request(calls),
      buildOptions(),
      async () => {
        throw new Error("不应执行");
      },
    );
    assert.equal(outcome.rejected, true);
    if (outcome.rejected) {
      assert.equal(outcome.error.type, "limit");
    }
  });

  it("成功调用的输出可被后续调用的结果引用使用", async () => {
    const seenArgs: Record<string, unknown>[] = [];
    const outcome = await dispatchAuthorized(
      request([
        ["Mailbox/query", { accountId: ACC_B }, "q"],
        [
          "Email/query",
          {
            accountId: ACC_B,
            filter: {
              inMailbox: {
                resultOf: "q",
                name: "Mailbox/query",
                path: "/ids/0",
              },
            },
          },
          "e",
        ],
      ]),
      buildOptions(),
      async ({ args, clientId }) => {
        seenArgs.push(args);
        if (clientId === "q") {
          return { ids: [MB_B1] };
        }
        return { ids: [EM_B1] };
      },
    );
    assert.equal(outcome.rejected, false);
    const emailArgs = seenArgs[1] as {
      filter: { inMailbox: string };
    };
    assert.equal(emailArgs.filter.inMailbox, MB_B1);
  });
});

describe("JSON Pointer 单元测试", () => {
  it("按 RFC 6901 解析对象、数组、转义与空指针", () => {
    const doc = {
      list: [{ "a/b": 1, "c~d": 2 }],
      empty: null,
    };
    assert.equal(resolvePointer(doc, ""), doc);
    assert.equal(resolvePointer(doc, "/list/0/a~1b"), 1);
    assert.equal(resolvePointer(doc, "/list/0/c~0d"), 2);
    assert.equal(resolvePointer(doc, "/empty"), null);
  });

  it("路径不存在或数组越界时抛错", () => {
    assert.throws(
      () => resolvePointer({ list: [1] }, "/list/3"),
      /越界|不存在/,
    );
    assert.throws(
      () => resolvePointer({ list: [1] }, "/missing/x"),
      /不存在/,
    );
    assert.throws(
      () => resolvePointer({ list: [1] }, "bad"),
      /JSON Pointer/,
    );
  });
});
