import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { Readable } from "node:stream";
import { sendWebResponse, toWebRequest } from "../index.ts";
import { accountsHandler, idAssertionHandler, sessionCookie } from "./demo-idp.ts";

interface FakeReqOptions {
  method: string;
  url: string;
  headers: Record<string, string>;
  body?: string;
}

function fakeIncomingMessage(options: FakeReqOptions): Readable {
  const stream =
    options.body === undefined
      ? Readable.from([])
      : Readable.from([Buffer.from(options.body, "utf8")]);
  return Object.assign(stream, {
    method: options.method,
    url: options.url,
    headers: options.headers,
  });
}

class FakeServerResponse {
  statusCode = 0;
  private headerMap = new Map<string, string>();
  private body = Buffer.alloc(0);

  setHeader(key: string, value: string): void {
    this.headerMap.set(key, value);
  }

  end(chunk?: Buffer): void {
    if (chunk !== undefined) {
      this.body = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    }
  }

  getHeader(key: string): string | undefined {
    return this.headerMap.get(key);
  }

  jsonBody(): unknown {
    return JSON.parse(this.body.toString("utf8"));
  }
}

describe("node:http 适配器", () => {
  it("把原生 IncomingMessage 适配为 Web Request 并跑通 accounts 处理器", async () => {
    const req = fakeIncomingMessage({
      method: "GET",
      url: "/fedcm/accounts",
      headers: {
        host: "idp.example",
        "sec-fetch-dest": "webidentity",
        cookie: sessionCookie("session-alice"),
      },
    });

    const request = await toWebRequest(req as never, "https");
    assert.equal(request.headers.get("sec-fetch-dest"), "webidentity");
    assert.equal(request.headers.get("cookie"), sessionCookie("session-alice"));

    const response = await accountsHandler(request);
    const res = new FakeServerResponse();
    await sendWebResponse(res as never, response);

    assert.equal(res.statusCode, 200);
    const payload = res.jsonBody() as { accounts: Array<{ id: string }> };
    assert.equal(payload.accounts.length, 2);
  });

it("适配 POST 请求体并跑通 id_assertion 处理器", async () => {
    const req = fakeIncomingMessage({
      method: "POST",
      url: "/fedcm/id_assertion",
      headers: {
        host: "idp.example",
        "sec-fetch-dest": "webidentity",
        "content-type": "application/json",
        origin: "https://rp.example",
        cookie: sessionCookie("session-alice"),
      },
      body: JSON.stringify({
        client_id: "rp-good",
        account_id: "acct-alice",
        disclosure_text_shown: true,
      }),
    });

    const request = await toWebRequest(req as never, "https");
    const response = await idAssertionHandler(request);
    const res = new FakeServerResponse();
    await sendWebResponse(res as never, response);

    assert.equal(res.statusCode, 200);
    const payload = res.jsonBody() as { token: string };
    assert.ok(payload.token.length > 0);
  });

  it("适配器保留错误头，校验失败同样映射为 403", async () => {
    const req = fakeIncomingMessage({
      method: "GET",
      url: "/fedcm/accounts",
      headers: { host: "idp.example", origin: "https://evil.example" },
    });

    const request = await toWebRequest(req as never, "https");
    const response = await accountsHandler(request);
    assert.equal(response.status, 403);
  });
});
