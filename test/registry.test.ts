import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  CrossOriginRedirectError,
  HandlerNotAuthorizedError,
  PaymentHandlerRegistry,
  type FetchLike,
  type FetchResponseLike,
  type PaymentMethodManifest,
} from "../src/registry.ts";

type Route =
  | { kind: "json"; body: PaymentMethodManifest }
  | { kind: "redirect"; location: string; status?: number }
  | { kind: "error"; status: number };

/** 模拟多源网络的假 fetcher，路由可按需替换以模拟撤销。 */
class FakeNetwork {
  readonly routes = new Map<string, Route>();

  setJson(url: string, body: PaymentMethodManifest): void {
    this.routes.set(url, { kind: "json", body });
  }

  setRedirect(url: string, location: string, status = 302): void {
    this.routes.set(url, { kind: "redirect", location, status });
  }

  setError(url: string, status: number): void {
    this.routes.set(url, { kind: "error", status });
  }

  readonly fetcher: FetchLike = async (input, init): Promise<FetchResponseLike> => {
    assert.equal(
      init?.redirect,
      "manual",
      "registry must request manual redirect handling",
    );
    const route = this.routes.get(input);
    if (route === undefined) {
      return {
        status: 404,
        ok: false,
        headers: { get: () => null },
        json: () => Promise.reject(new Error("no body")),
      };
    }
    switch (route.kind) {
      case "json":
        return {
          status: 200,
          ok: true,
          headers: { get: () => null },
          json: () => Promise.resolve(route.body),
        };
      case "redirect":
        return {
          status: route.status ?? 302,
          ok: false,
          headers: {
            get: (name) => (name.toLowerCase() === "location" ? route.location : null),
          },
          json: () => Promise.reject(new Error("no body")),
        };
      case "error":
        return {
          status: route.status,
          ok: false,
          headers: { get: () => null },
          json: () => Promise.reject(new Error("no body")),
        };
    }
  };
}

const METHOD_URL = "https://pay.example.com/pay/method.json";
const HANDLER_ORIGIN = "https://wallet.example.com";
const ATTACKER_ORIGIN = "https://evil.example.net";

describe("清单获取与重定向", () => {
  it("拒绝跨源重定向：攻击者无法把方式 URL 劫持到他源清单", async () => {
    const net = new FakeNetwork();
    // 方式 URL 所属源把请求 302 到攻击者源上的“清单”。
    net.setRedirect(METHOD_URL, `${ATTACKER_ORIGIN}/fake-manifest.json`);
    net.setJson(`${ATTACKER_ORIGIN}/fake-manifest.json`, {
      supported_origins: [ATTACKER_ORIGIN],
    });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await assert.rejects(
      registry.register(METHOD_URL, ATTACKER_ORIGIN),
      (error: unknown) => {
        assert.ok(error instanceof CrossOriginRedirectError);
        return true;
      },
    );
    assert.equal(registry.canHandle(METHOD_URL, ATTACKER_ORIGIN), false);
  });

  it("允许同源重定向并正常注册", async () => {
    const net = new FakeNetwork();
    net.setRedirect(METHOD_URL, "https://pay.example.com/pay/v2/method.json", 301);
    net.setJson("https://pay.example.com/pay/v2/method.json", {
      supported_origins: [HANDLER_ORIGIN],
    });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), true);
  });

  it("相对同源重定向同样允许", async () => {
    const net = new FakeNetwork();
    net.setRedirect(METHOD_URL, "/pay/v3/method.json");
    net.setJson("https://pay.example.com/pay/v3/method.json", {
      default_applications: [`${HANDLER_ORIGIN}/manifest.webmanifest`],
    });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), true);
  });
});

describe("handler 来源授权", () => {
  it("supported_origins 缺项：拒绝注册", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, {
      supported_origins: ["https://other-wallet.example.org"],
    });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await assert.rejects(
      registry.register(METHOD_URL, ATTACKER_ORIGIN),
      HandlerNotAuthorizedError,
    );
    assert.equal(registry.canHandle(METHOD_URL, ATTACKER_ORIGIN), false);
  });

  it("default_applications 与 supported_origins 均缺项：拒绝注册", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, {
      default_applications: ["https://pay.example.com/app.webmanifest"],
      supported_origins: [],
    });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await assert.rejects(
      registry.register(METHOD_URL, ATTACKER_ORIGIN),
      HandlerNotAuthorizedError,
    );
  });

  it("default_applications 列出的来源允许注册", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, {
      default_applications: [`${HANDLER_ORIGIN}/manifest.webmanifest`],
    });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), true);
  });

  it("supported_origins 为 \"*\" 时允许任意来源", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, { supported_origins: "*" });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), true);
  });
});

describe("清单撤销", () => {
  it("清单被撤销（404）后 handler 不再响应", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, { supported_origins: [HANDLER_ORIGIN] });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), true);

    // 服务端撤销清单。
    net.setError(METHOD_URL, 404);
    const remaining = await registry.refresh(METHOD_URL);

    assert.deepEqual(remaining, []);
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), false);
  });

  it("清单变更移除授权后 handler 不再响应", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, { supported_origins: [HANDLER_ORIGIN] });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);

    // 清单更新，不再列出该 handler 来源。
    net.setJson(METHOD_URL, { supported_origins: ["https://other.example.org"] });
    await registry.refresh(METHOD_URL);

    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), false);
  });

  it("撤销后重新注册被拒绝", async () => {
    const net = new FakeNetwork();
    net.setJson(METHOD_URL, { supported_origins: [HANDLER_ORIGIN] });
    const registry = new PaymentHandlerRegistry({ fetcher: net.fetcher });

    await registry.register(METHOD_URL, HANDLER_ORIGIN);
    net.setError(METHOD_URL, 410);
    await registry.refresh(METHOD_URL);

    await assert.rejects(registry.register(METHOD_URL, HANDLER_ORIGIN));
    assert.equal(registry.canHandle(METHOD_URL, HANDLER_ORIGIN), false);
  });
});
