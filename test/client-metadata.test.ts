import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { clientMetadataHandler } from "./demo-idp.ts";

const METADATA_URL = "https://idp.example/fedcm/client_metadata?client_id=rp-good";

function metadataRequest(headers: Record<string, string> = {}): Request {
  return new Request(METADATA_URL, {
    method: "GET",
    headers: { "sec-fetch-dest": "webidentity", ...headers },
  });
}

describe("client_metadata 端点", () => {
  it("无 Cookie 的合法请求返回登记的元数据", async () => {
    const response = await clientMetadataHandler(metadataRequest());
    assert.equal(response.status, 200);
    const payload = await response.json();
    assert.equal(payload.privacy_policy_url, "https://idp.example/privacy");
  });

  it("带 Cookie 的元数据请求被拒绝", async () => {
    const response = await clientMetadataHandler(
      metadataRequest({ cookie: "sid=session-alice" }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "cookie_forbidden");
  });

  it("缺失 Sec-Fetch-Dest 时被拒绝", async () => {
    const response = await clientMetadataHandler(new Request(METADATA_URL));
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "not_webidentity");
  });

  it("带 Origin 的元数据请求被拒绝", async () => {
    const response = await clientMetadataHandler(
      metadataRequest({ origin: "https://rp.example" }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "origin_forbidden");
  });
});
