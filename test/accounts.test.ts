import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  accountsHandler,
  redirectingHandler,
  sessionCookie,
} from "./demo-idp.ts";

const ACCOUNTS_URL = "https://idp.example/fedcm/accounts";

function fedcmGet(headers: Record<string, string> = {}): Request {
  return new Request(ACCOUNTS_URL, {
    method: "GET",
    headers: { "sec-fetch-dest": "webidentity", ...headers },
  });
}

describe("accounts 端点", () => {
  it("合法 FedCM 请求凭 Cookie 返回会话名下的账户列表", async () => {
    const response = await accountsHandler(
      fedcmGet({ cookie: sessionCookie("session-alice") }),
    );
    assert.equal(response.status, 200);
    const payload = await response.json();
    assert.deepEqual(
      payload.accounts.map((account: { id: string }) => account.id),
      ["acct-alice", "acct-alice-work"],
    );
  });

  it("无会话 Cookie 时返回空账户列表而非报错", async () => {
    const response = await accountsHandler(fedcmGet());
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { accounts: [] });
  });

  it("页面直接 fetch（缺失 Sec-Fetch-Dest）被拒绝", async () => {
    const request = new Request(ACCOUNTS_URL, { method: "GET" });
    assert.equal(request.headers.get("sec-fetch-dest"), null);

    const response = await accountsHandler(request);
    assert.equal(response.status, 403);
    const payload = await response.json();
    assert.equal(payload.error.reason, "not_webidentity");
  });

  it("Sec-Fetch-Dest 为其他值时被拒绝", async () => {
    const response = await accountsHandler(
      new Request(ACCOUNTS_URL, {
        method: "GET",
        headers: { "sec-fetch-dest": "empty" },
      }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "not_webidentity");
  });

  it("携带 Origin 的请求被拒绝（攻击者页面 fetch）", async () => {
    const response = await accountsHandler(
      fedcmGet({ origin: "https://evil.example" }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "origin_forbidden");
  });

  it("携带 Referer 的请求被拒绝", async () => {
    const response = await accountsHandler(
      fedcmGet({ referer: "https://evil.example/page" }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "referer_forbidden");
  });

  it("端点处理器返回 3xx 时重定向被拦截并改写为 500", async () => {
    const response = await redirectingHandler(fedcmGet());
    assert.equal(response.status, 500);
    assert.equal((await response.json()).error.reason, "redirect_forbidden");
  });
});
