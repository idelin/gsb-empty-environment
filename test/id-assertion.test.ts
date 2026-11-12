import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { idAssertionHandler, sessionCookie } from "./demo-idp.ts";

const URL_ASSERTION = "https://idp.example/fedcm/id_assertion";

interface AssertionRequestInit {
  origin?: string;
  cookie?: string;
  secFetchDest?: string;
  contentType?: string;
  method?: string;
}

function assertionRequest(
  body: Record<string, unknown>,
  init: AssertionRequestInit = {},
): Request {
  const method = init.method ?? "POST";
  return new Request(URL_ASSERTION, {
    method,
    headers: {
      "sec-fetch-dest": init.secFetchDest ?? "webidentity",
      "content-type": init.contentType ?? "application/json",
      ...(init.origin !== undefined ? { origin: init.origin } : {}),
      ...(init.cookie !== undefined ? { cookie: init.cookie } : {}),
    },
    ...(method === "GET" || method === "HEAD" ? {} : { body: JSON.stringify(body) }),
  });
}

const validBody = {
  client_id: "rp-good",
  account_id: "acct-alice",
  disclosure_text_shown: true,
  nonce: "n-1",
};

describe("id_assertion 端点", () => {
  it("来源与账户归属均合法时签发断言", async () => {
    const response = await idAssertionHandler(
      assertionRequest(validBody, {
        origin: "https://rp.example",
        cookie: sessionCookie("session-alice"),
      }),
    );
    assert.equal(response.status, 200);
    const payload = await response.json();
    assert.match(payload.token, /^[\w-]+$/);
  });

  it("非 POST 请求被拒绝（405）", async () => {
    const response = await idAssertionHandler(
      assertionRequest(validBody, {
        method: "GET",
        origin: "https://rp.example",
        cookie: sessionCookie("session-alice"),
      }),
    );
    assert.equal(response.status, 405);
    assert.equal((await response.json()).error.reason, "method_not_allowed");
  });

  it("缺失 Sec-Fetch-Dest: webidentity 时被拒绝", async () => {
    const request = new Request(URL_ASSERTION, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        origin: "https://rp.example",
        cookie: sessionCookie("session-alice"),
      },
      body: JSON.stringify(validBody),
    });
    const response = await idAssertionHandler(request);
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "not_webidentity");
  });

  it("错误来源（未登记的 Origin）被拒绝", async () => {
    const response = await idAssertionHandler(
      assertionRequest(validBody, {
        origin: "https://evil.example",
        cookie: sessionCookie("session-alice"),
      }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "origin_not_allowed");
  });

  it("冒用其他依赖方来源：rp-partner 的来源对 rp-good 无效", async () => {
    const response = await idAssertionHandler(
      assertionRequest(validBody, {
        origin: "https://partner.example",
        cookie: sessionCookie("session-alice"),
      }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "origin_not_allowed");
  });

  it("登记来源做默认端口归一化后仍可通过", async () => {
    const response = await idAssertionHandler(
      assertionRequest(
        { ...validBody, client_id: "rp-partner" },
        {
          origin: "https://partner.example:443/path",
          cookie: sessionCookie("session-alice"),
        },
      ),
    );
    assert.equal(response.status, 200);
  });

  it("缺失 Origin 头被拒绝", async () => {
    const response = await idAssertionHandler(
      assertionRequest(validBody, { cookie: sessionCookie("session-alice") }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "origin_not_allowed");
  });

  it("未知 client_id 被拒绝", async () => {
    const response = await idAssertionHandler(
      assertionRequest(
        { ...validBody, client_id: "rp-ghost" },
        {
          origin: "https://rp.example",
          cookie: sessionCookie("session-alice"),
        },
      ),
    );
    assert.equal(response.status, 400);
    assert.equal((await response.json()).error.reason, "unknown_client");
  });

  it("他人 account_id（不属于 Cookie 对应会话）被拒绝", async () => {
    const response = await idAssertionHandler(
      assertionRequest(
        { ...validBody, account_id: "acct-bob" },
        {
          origin: "https://rp.example",
          cookie: sessionCookie("session-alice"),
        },
      ),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "account_not_in_session");
  });

  it("Bob 会话只能签发 Bob 自己的账户断言", async () => {
    const response = await idAssertionHandler(
      assertionRequest(
        { ...validBody, account_id: "acct-bob" },
        {
          origin: "https://rp.example",
          cookie: sessionCookie("session-bob"),
        },
      ),
    );
    assert.equal(response.status, 200);
  });

  it("无会话 Cookie 被拒绝（401）", async () => {
    const response = await idAssertionHandler(
      assertionRequest(validBody, { origin: "https://rp.example" }),
    );
    assert.equal(response.status, 401);
    assert.equal((await response.json()).error.reason, "session_not_found");
  });

  it("非 JSON 请求体被拒绝（415/400）", async () => {
    const request = new Request(URL_ASSERTION, {
      method: "POST",
      headers: {
        "sec-fetch-dest": "webidentity",
        "content-type": "text/plain",
        origin: "https://rp.example",
        cookie: sessionCookie("session-alice"),
      },
      body: "not-json",
    });
    const response = await idAssertionHandler(request);
    assert.equal(response.status, 415);
    assert.equal((await response.json()).error.reason, "unsupported_media_type");
  });
});
