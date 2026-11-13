import assert from "node:assert/strict";
import test from "node:test";

import {
  admitSubgraphRequest,
  signGatewayRequest,
  type EntityAuthorizationInput,
  type RequestAdmissionOptions,
} from "../index.ts";

const SECRET = "gateway-test-secret";
const NOW = 1_800_000_000_000;

const ENTITY_QUERY = `
  query Entities($representations: [_Any!]!) {
    _entities(representations: $representations) {
      ... on Product {
        __typename
        id
        name
        secret
      }
    }
  }
`;

function optionsWith(
  overrides: Partial<RequestAdmissionOptions> = {},
): RequestAdmissionOptions {
  return {
    secret: SECRET,
    entities: {
      Product: { keyFields: ["id"] },
      User: { keyFields: ["id"] },
    },
    production: false,
    now: () => NOW,
    ...overrides,
  };
}

function signedRequest(
  query: string,
  variables: Readonly<Record<string, unknown>> = {},
  timestamp = NOW,
): { readonly body: string; readonly headers: Record<string, string> } {
  const body = JSON.stringify({ query, variables });
  const signed = signGatewayRequest(SECRET, body, { timestamp });
  return { body, headers: signed.headers };
}

function productRepresentations(
  count: number,
): Readonly<Record<string, unknown>>[] {
  return Array.from({ length: count }, (_, index) => ({
    __typename: "Product",
    id: `product-${index}`,
  }));
}

test("rejects an unsigned direct _entities query", async () => {
  const body = JSON.stringify({
    query: ENTITY_QUERY,
    variables: {
      representations: [{ __typename: "Product", id: "product-1" }],
    },
  });

  const result = await admitSubgraphRequest(optionsWith(), {
    headers: {},
    body,
  });

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.status, 401);
    assert.equal(result.code, "MISSING_GATEWAY_SIGNATURE");
  }
});

test("rejects an unregistered representation __typename", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [{ __typename: "Admin", id: "admin-1" }],
  });
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "UNKNOWN_ENTITY_TYPE");
  }
});

test("rejects representation fields outside the declared key", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [
      { __typename: "Product", id: "product-1", name: "leaked" },
    ],
  });
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "EXTRA_ENTITY_FIELD");
    assert.match(result.message, /outside its declared entity key/);
  }
});

test("rejects a gateway signature with an expired timestamp", async () => {
  const request = signedRequest(
    ENTITY_QUERY,
    { representations: [{ __typename: "Product", id: "product-1" }] },
    NOW - 31_000,
  );
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.status, 401);
    assert.equal(result.code, "STALE_GATEWAY_TIMESTAMP");
  }
});

test("admits a valid gateway request and denies entity fields one by one", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [
      { __typename: "Product", id: "product-1" },
      { __typename: "Product", id: "product-2" },
    ],
  });
  const authorize = (input: EntityAuthorizationInput): boolean =>
    input.field !== "secret";
  const result = await admitSubgraphRequest(
    optionsWith({ authorize }),
    request,
  );

  assert.equal(result.ok, true);
  if (!result.ok) {
    return;
  }
  assert.equal(result.operationName, "Entities");
  assert.equal(result.entityRequests.length, 2);
  assert.deepEqual(
    result.entityRequests.map((item) => item.typename),
    ["Product", "Product"],
  );

  const authorized = await result.authorizeEntity(
    "Product",
    {
      __typename: "Product",
      id: "product-1",
      name: "Widget",
      secret: "internal-only",
    },
    0,
  );

  assert.deepEqual(authorized.data, {
    __typename: "Product",
    id: "product-1",
    name: "Widget",
  });
  assert.deepEqual(authorized.entity, {
    __typename: "Product",
    id: "product-1",
    name: "Widget",
  });
  assert.deepEqual(authorized.deniedFields, ["secret"]);
  assert.equal(authorized.errors.length, 1);
  assert.deepEqual(authorized.errors[0].path, ["_entities", 0, "secret"]);
  assert.equal("secret" in (authorized.data ?? {}), false);
});

test("rejects _service requests without a gateway signature", async () => {
  const result = await admitSubgraphRequest(optionsWith(), {
    headers: {},
    body: JSON.stringify({ query: "{ _service { sdl } }", variables: {} }),
  });

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "MISSING_GATEWAY_SIGNATURE");
  }
});

test("rejects arrays as GraphQL batch requests", async () => {
  const batchBody = JSON.stringify([
    { query: ENTITY_QUERY, variables: { representations: productRepresentations(1) } },
    { query: "{ _service { sdl } }", variables: {} },
  ]);
  const signed = signGatewayRequest(SECRET, batchBody, { timestamp: NOW });
  const result = await admitSubgraphRequest(optionsWith(), {
    headers: signed.headers,
    body: batchBody,
  });

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "BATCH_NOT_ALLOWED");
  }
});

test("rejects introspection in production", async () => {
  const request = signedRequest("{ __schema { types { name } } }");
  const result = await admitSubgraphRequest(
    optionsWith({ production: true }),
    request,
  );

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.status, 403);
    assert.equal(result.code, "INTROSPECTION_DISABLED");
  }
});

test("rejects query depth greater than twelve", async () => {
  let selection = "leaf";
  for (let index = 11; index >= 0; index -= 1) {
    selection = `field${index} { ${selection} }`;
  }
  const request = signedRequest(`{ ${selection} }`);
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "QUERY_TOO_DEEP");
    assert.equal(result.details?.depth, 13);
  }
});

test("rejects more than one hundred representations", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: productRepresentations(101),
  });
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.status, 413);
    assert.equal(result.code, "TOO_MANY_REPRESENTATIONS");
  }
});

test("rejects a representation missing its declared key field", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [{ __typename: "Product" }],
  });
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "INVALID_ENTITY_KEY");
  }
});

test("rejects a body changed after the gateway signed it", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [{ __typename: "Product", id: "product-1" }],
  });
  const tampered = request.body.replace("product-1", "product-2");
  const result = await admitSubgraphRequest(optionsWith(), {
    headers: request.headers,
    body: tampered,
  });

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "INVALID_GATEWAY_SIGNATURE");
  }
});

test("accepts exactly one hundred representations and the thirty-second boundary", async () => {
  const request = signedRequest(
    ENTITY_QUERY,
    { representations: productRepresentations(100) },
    NOW - 30_000,
  );
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.entityRequests.length, 100);
  }
});

test("validates inline representation literals", async () => {
  const query = `
    query Inline {
      _entities(
        representations: [{ __typename: "Product", id: "product-1" }]
      ) {
        ... on Product { id }
      }
    }
  `;
  const request = signedRequest(query);
  const result = await admitSubgraphRequest(optionsWith(), request);

  assert.equal(result.ok, true);
  if (result.ok) {
    assert.deepEqual(result.entityRequests[0].representation, {
      __typename: "Product",
      id: "product-1",
    });
  }
});

test("resolves entity selections through named fragments", async () => {
  const query = `
    query Fragmented($representations: [_Any!]!) {
      _entities(representations: $representations) {
        ...ProductFields
      }
    }
    fragment ProductFields on Product {
      __typename
      id
      name
    }
  `;
  const request = signedRequest(query, {
    representations: [{ __typename: "Product", id: "product-1" }],
  });
  const result = await admitSubgraphRequest(
    optionsWith({ authorize: ({ field }: EntityAuthorizationInput) => field !== "name" }),
    request,
  );

  assert.equal(result.ok, true);
  if (!result.ok) {
    return;
  }
  const authorized = await result.authorizeEntity(
    "Product",
    { __typename: "Product", id: "product-1", name: "Widget" },
    0,
  );
  assert.deepEqual(authorized.data, { __typename: "Product", id: "product-1" });
  assert.deepEqual(authorized.deniedFields, ["name"]);
});

test("supports a positional two-argument authorization callback", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [{ __typename: "Product", id: "product-1" }],
  });
  const authorize = (typename: string, field: string): boolean =>
    typename === "Product" && field === "id";
  const result = await admitSubgraphRequest(
    optionsWith({ authorizeField: authorize }),
    request,
  );

  assert.equal(result.ok, true);
  if (!result.ok) {
    return;
  }
  const authorized = await result.authorizeEntity(
    "Product",
    { __typename: "Product", id: "product-1", name: "Widget", secret: "hidden" },
    0,
  );
  assert.deepEqual(authorized.data, { __typename: "Product", id: "product-1" });
  assert.deepEqual(authorized.deniedFields, ["name", "secret"]);
});

test("applies the entity public field whitelist per field", async () => {
  const request = signedRequest(ENTITY_QUERY, {
    representations: [{ __typename: "Product", id: "product-1" }],
  });
  const result = await admitSubgraphRequest(
    optionsWith({
      entities: { Product: { keyFields: ["id"], fields: ["id", "name"] } },
      authorize: () => true,
    }),
    request,
  );

  assert.equal(result.ok, true);
  if (!result.ok) {
    return;
  }
  const authorized = await result.authorizeEntity(
    "Product",
    { __typename: "Product", id: "product-1", name: "Widget", secret: "hidden" },
    0,
  );
  assert.deepEqual(authorized.data, {
    __typename: "Product",
    id: "product-1",
    name: "Widget",
  });
  assert.deepEqual(authorized.deniedFields, ["secret"]);
});
