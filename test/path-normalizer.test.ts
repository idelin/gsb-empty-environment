import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  PathNormalizationError,
  UNIFORM_DENIAL,
  authorizeRequest,
  handleRequest,
  matchRoute,
  normalizeRequestTarget,
} from "../index.ts";

const TENANT_POLICY = Object.freeze({
  allowedPrefixes: Object.freeze(["/api/tenant/"]),
});

const ROUTES = Object.freeze([
  Object.freeze({
    path: "/api/tenant/secret",
    resource: "tenant:secret",
    methods: Object.freeze(["GET"]),
  }),
  Object.freeze({
    path: "/api/tenant/profile",
    resource: "tenant:profile",
    methods: Object.freeze(["GET"]),
  }),
  Object.freeze({
    path: "/api/admin/secret",
    resource: "admin:secret",
    methods: Object.freeze(["GET"]),
  }),
]);

describe("normalizeRequestTarget", () => {
  it("keeps matrix parameters attached to their segment", () => {
    const path = normalizeRequestTarget(
      "/api/tenant/resource;version=2;preview",
    );

    assert.equal(path.path, "/api/tenant/resource;version=2;preview");
    assert.equal(path.resourcePath, "/api/tenant/resource");
    assert.equal(path.hasMatrix, true);
    assert.deepEqual(path.matrixSegments[0]?.matrix, [
      { name: "version", value: "2", raw: "version=2" },
      { name: "preview", value: null, raw: "preview" },
    ]);
  });

  it("treats a percent-encoded semicolon as a real matrix delimiter", () => {
    const literal = normalizeRequestTarget("/api/tenant;admin/secret");
    const encoded = normalizeRequestTarget("/api/tenant%3Badmin/secret");

    assert.equal(encoded.path, literal.path);
    assert.equal(encoded.resourcePath, literal.resourcePath);
    assert.equal(encoded.path, "/api/tenant;admin/secret");
    assert.equal(encoded.resourcePath, "/api/tenant/secret");
    assert.equal(encoded.segments[1]?.hasMatrix, true);
  });

  it("collapses duplicate and empty segments while retaining a trailing slash", () => {
    const path = normalizeRequestTarget("//api//tenant///secret//");

    assert.equal(path.path, "/api/tenant/secret/");
    assert.equal(path.resourcePath, "/api/tenant/secret/");
    assert.equal(path.trailingSlash, true);
    assert.ok(path.collapsedEmptySegments >= 3);
    assert.deepEqual(path.segmentNames, ["api", "tenant", "secret"]);
  });

  it("decodes percent-encoded separators exactly once", () => {
    const path = normalizeRequestTarget("/api%2Ftenant%2Fsecret");
    assert.equal(path.path, "/api/tenant/secret");
    assert.equal(path.resourcePath, "/api/tenant/secret");

    assert.throws(
      () => normalizeRequestTarget("/api%252Ftenant%252Fsecret"),
      (error: unknown) =>
        error instanceof PathNormalizationError &&
        error.code === "DOUBLE_ENCODED_DELIMITER",
    );
  });

  it("case-folds path names and removes trailing dots", () => {
    const path = normalizeRequestTarget("/API/Tenant./SECRET.");

    assert.equal(path.path, "/api/tenant/secret");
    assert.equal(path.resourcePath, "/api/tenant/secret");
    assert.deepEqual(path.segmentNames, ["api", "tenant", "secret"]);
  });

  it("resolves decoded dot segments before authorization", () => {
    const path = normalizeRequestTarget("/api/tenant/%2e%2e/admin/secret");

    assert.equal(path.path, "/api/admin/secret");
    assert.equal(path.resourcePath, "/api/admin/secret");
    assert.deepEqual(path.segmentNames, ["api", "admin", "secret"]);
  });

  it("rejects malformed percent encoding instead of guessing a path", () => {
    assert.throws(
      () => normalizeRequestTarget("/api/tenant/%2"),
      (error: unknown) =>
        error instanceof PathNormalizationError &&
        error.code === "INVALID_PERCENT_ENCODING",
    );
  });
});

describe("authorization and routing use the same path", () => {
  it("authorizes a legitimate tenant resource and resolves the same resource", () => {
    const request = handleRequest(
      { method: "GET", target: "/api/tenant/secret" },
      { policy: TENANT_POLICY, routes: ROUTES },
    );

    assert.equal(request.allowed, true);
    assert.equal(request.path, "/api/tenant/secret");
    assert.equal(request.resourcePath, "/api/tenant/secret");
    assert.equal(request.route?.resource, "tenant:secret");
    assert.equal(request.normalized?.resourcePath, request.route?.path);
  });

  it("routes a resource matrix parameter to the same resource the backend sees", () => {
    const request = handleRequest(
      { method: "GET", target: "/api/tenant/secret;version=2" },
      { policy: TENANT_POLICY, routes: ROUTES },
    );

    assert.equal(request.allowed, true);
    assert.equal(request.path, "/api/tenant/secret;version=2");
    assert.equal(request.resourcePath, "/api/tenant/secret");
    assert.equal(request.route?.path, "/api/tenant/secret");
    assert.equal(request.normalized?.resourcePath, request.route?.path);
  });

  it("treats an encoded matrix delimiter below the prefix like a literal one", () => {
    const request = handleRequest(
      { method: "GET", target: "/api/tenant/secret%3Bversion=2" },
      { policy: TENANT_POLICY, routes: ROUTES },
    );

    assert.equal(request.allowed, true);
    assert.equal(request.path, "/api/tenant/secret;version=2");
    assert.equal(request.resourcePath, "/api/tenant/secret");
    assert.equal(request.normalized?.resourcePath, request.route?.path);
  });

  it("denies a matrix parameter that changes the authorization segment", () => {
    for (const target of [
      "/api/tenant;admin/secret",
      "/api/tenant%3Badmin/secret",
      "/API/TENANT;ADMIN/SECRET",
    ]) {
      const decision = authorizeRequest(target, TENANT_POLICY);

      assert.equal(decision.allowed, false);
      assert.deepEqual(
        {
          status: decision.status,
          body: decision.body,
          headers: decision.headers,
        },
        {
          status: UNIFORM_DENIAL.status,
          body: UNIFORM_DENIAL.body,
          headers: UNIFORM_DENIAL.headers,
        },
      );
      assert.equal(decision.path, undefined);
    }
  });

  it("normalizes duplicate slashes, casing, and trailing dots to the same resource", () => {
    for (const target of [
      "/api//tenant///secret",
      "/API/TENANT/SECRET",
      "/api/tenant./secret.",
      "/api%2Ftenant%2Fsecret",
    ]) {
      const decision = authorizeRequest(target, TENANT_POLICY);
      assert.equal(decision.allowed, true, target);
      assert.equal(decision.resourcePath, "/api/tenant/secret", target);

      const route = matchRoute(decision.normalized!, ROUTES, "GET");
      assert.equal(route?.path, decision.resourcePath, target);
      assert.equal(route?.resource, "tenant:secret", target);
    }
  });

  it("denies traversal and out-of-tenant paths with the uniform response", () => {
    const denied = [
      "/api/admin/secret",
      "/api/tenant/../admin/secret",
      "/api/tenant/%2e%2e/admin/secret",
      "/api/tenant/secret/../../admin/secret",
      "/api/tenantx/secret",
    ];

    for (const target of denied) {
      assert.deepEqual(
        handleRequest({ method: "GET", target }, {
          policy: TENANT_POLICY,
          routes: ROUTES,
        }),
        UNIFORM_DENIAL,
        target,
      );
    }
  });

  it("returns the same denial for malformed targets and unknown routes", () => {
    assert.deepEqual(
      handleRequest({ method: "GET", target: "/api/tenant/%" }, {
        policy: TENANT_POLICY,
        routes: ROUTES,
      }),
      UNIFORM_DENIAL,
    );
    assert.deepEqual(
      handleRequest({ method: "GET", target: "/api/tenant/missing" }, {
        policy: TENANT_POLICY,
        routes: ROUTES,
      }),
      UNIFORM_DENIAL,
    );
  });

  it("compares prefixes by path segment, not by string prefix", () => {
    assert.equal(
      authorizeRequest("/api/tenantx/secret", TENANT_POLICY).allowed,
      false,
    );
    assert.equal(
      authorizeRequest("/api/tenant", TENANT_POLICY).allowed,
      false,
    );
    assert.equal(
      authorizeRequest("/api/tenant/", TENANT_POLICY).allowed,
      true,
    );
  });
});
