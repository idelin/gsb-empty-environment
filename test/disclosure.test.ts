import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  decodeToken,
  idAssertionHandler,
  sessionCookie,
} from "./demo-idp.ts";
import { assertEmailDisclosure, FedcmError, withEmailClaim } from "../index.ts";

const URL_ASSERTION = "https://idp.example/fedcm/id_assertion";

function postAssertion(body: Record<string, unknown>): Request {
  return new Request(URL_ASSERTION, {
    method: "POST",
    headers: {
      "sec-fetch-dest": "webidentity",
      "content-type": "application/json",
      origin: "https://rp.example",
      cookie: sessionCookie("session-alice"),
    },
    body: JSON.stringify(body),
  });
}

const baseBody = { client_id: "rp-good", account_id: "acct-alice" };

describe("披露文本与邮箱断言", () => {
  it("disclosure_text_shown 为 true 时签发含邮箱的断言", async () => {
    const response = await idAssertionHandler(
      postAssertion({ ...baseBody, disclosure_text_shown: true }),
    );
    assert.equal(response.status, 200);
    const payload = await response.json();
    const claims = decodeToken<{ email: string; sub: string }>(payload.token);
    assert.equal(claims.email, "alice@idp.example");
    assert.equal(claims.sub, "acct-alice");
  });

  it("缺失 disclosure_text_shown 时不得发放含邮箱的断言", async () => {
    const response = await idAssertionHandler(postAssertion({ ...baseBody }));
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "disclosure_required");
  });

  it("disclosure_text_shown 为 false 时不得发放含邮箱的断言", async () => {
    const response = await idAssertionHandler(
      postAssertion({ ...baseBody, disclosure_text_shown: false }),
    );
    assert.equal(response.status, 403);
    assert.equal((await response.json()).error.reason, "disclosure_required");
  });

  it("assertEmailDisclosure 在未披露且含邮箱时抛出 FedcmError", () => {
    assert.throws(
      () => assertEmailDisclosure({ sub: "a", email: "a@example.com" }, false),
      (error: unknown) => error instanceof FedcmError && error.reason === "disclosure_required",
    );
    assert.throws(
      () => assertEmailDisclosure({ sub: "a", email: "a@example.com" }, false),
      { status: 403 },
    );
  });

  it("无邮箱载荷不受披露标记约束", () => {
    assert.doesNotThrow(() => assertEmailDisclosure({ sub: "a" }, false));
  });

  it("withEmailClaim 在未披露时自动剔除邮箱，披露时保留", () => {
    const hidden = withEmailClaim({ sub: "a" }, "a@example.com", false);
    assert.equal(hidden.email, undefined);

    const shown = withEmailClaim({ sub: "a" }, "a@example.com", true);
    assert.equal(shown.email, "a@example.com");
  });
});
