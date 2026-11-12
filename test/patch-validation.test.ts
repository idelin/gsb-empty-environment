import test from "node:test";
import assert from "node:assert/strict";

import {
  CORE_USER_SCHEMA,
  CORE_USER_SCHEMA_URN,
  ENTERPRISE_USER_SCHEMA,
  ENTERPRISE_USER_SCHEMA_URN,
  ScimPatchValidationError,
  assertPatchAllowed,
  parseScimPath,
  validatePatch,
} from "../index.ts";

const schemas = [CORE_USER_SCHEMA, ENTERPRISE_USER_SCHEMA];

const writable = [
  "userName",
  "name",
  "active",
  "emails",
  `${ENTERPRISE_USER_SCHEMA_URN}:department`,
];

function validate(
  operations: readonly unknown[],
  writableAttributes: readonly string[] = writable,
): ReturnType<typeof validatePatch> {
  return validatePatch(
    {
      schemas: ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
      Operations: operations,
    },
    { schemas, writableAttributes },
  );
}

test("parses filtered paths, sub-attributes, and case-insensitive names", () => {
  const path = parseScimPath('EMAILS[TYPE EQ "WORK"].VALUE', [
    ENTERPRISE_USER_SCHEMA_URN,
  ]);

  assert.deepEqual(path.attributeSegments, ["emails"]);
  assert.deepEqual(path.targetSegments, ["emails", "value"]);
  assert.equal(path.filter?.attribute, "type");
  assert.equal(path.filter?.op, "eq");
  assert.equal(path.filter?.value, "WORK");

  const enterprisePath = parseScimPath(
    `${ENTERPRISE_USER_SCHEMA_URN.toUpperCase()}:MANAGER.VALUE`,
    schemas.map((schema) => schema.id as string),
  );
  assert.equal(enterprisePath.schemaUrn, ENTERPRISE_USER_SCHEMA_URN);
  assert.deepEqual(enterprisePath.targetSegments, ["manager", "value"]);
});

test("allows permitted active, filtered email, and enterprise extension paths", () => {
  const result = validate([
    { op: "replace", path: "active", value: false },
    {
      op: "replace",
      path: 'emails[type eq "work"].value',
      value: "work@example.com",
    },
    {
      op: "replace",
      path: `${ENTERPRISE_USER_SCHEMA_URN}:department`,
      value: "Engineering",
    },
  ]);

  assert.equal(result.valid, true);
  assert.deepEqual(result.violations, []);
});

test("allows the same attributes with different casing", () => {
  const result = validate([
    { op: "REPLACE", path: "ACTIVE", value: true },
    {
      op: "Replace",
      path: 'EMAILS[TYPE EQ "WORK"].VALUE',
      value: "work@example.com",
    },
    {
      op: "replace",
      path: `${ENTERPRISE_USER_SCHEMA_URN.toUpperCase()}:DEPARTMENT`,
      value: "Engineering",
    },
  ]);

  assert.equal(result.valid, true);
});

test("pathless replace rejects smuggled roles, groups, and meta as one batch", () => {
  const result = validate([
    {
      op: "replace",
      value: {
        active: false,
        userName: "alice",
        ROLES: [{ value: "admin" }],
        groups: [{ value: "admins" }],
        meta: { version: "W/\"1\"" },
      },
    },
  ]);

  assert.equal(result.valid, false);
  const reasons = new Set(result.violations.map((violation) => violation.reason));
  assert.ok(reasons.has("notWritable") || reasons.has("readOnly"));
  assert.ok(
    result.violations.some((violation) =>
      violation.attributePath.startsWith("roles")
    ),
  );
  assert.ok(
    result.violations.some((violation) =>
      violation.attributePath.startsWith("groups")
    ),
  );
  assert.ok(
    result.violations.some((violation) =>
      violation.attributePath.startsWith("meta")
    ),
  );
});

test("read-only schema mutability wins even over an explicit writable grant", () => {
  const result = validate(
    [
      { op: "replace", path: "groups", value: [{ value: "admins" }] },
      { op: "replace", path: "meta.version", value: "W/\"2\"" },
      { op: "replace", path: "roles", value: [{ value: "admin" }] },
    ],
    ["groups", "meta", "roles"],
  );

  assert.equal(result.valid, false);
  assert.ok(result.violations.every((violation) => violation.reason === "readOnly"));
});

test("filtered paths cannot smuggle sibling attributes in the value object", () => {
  const result = validate([
    {
      op: "replace",
      path: 'emails[type eq "work"]',
      value: {
        value: "work@example.com",
        roles: [{ value: "admin" }],
      },
    },
  ]);

  assert.equal(result.valid, false);
  assert.ok(
    result.violations.some((violation) =>
      violation.attributePath === "emails.roles"
    ),
  );
});

test("pathless extension container expands before authorization checks", () => {
  const allowed = validate([
    {
      op: "replace",
      value: {
        [ENTERPRISE_USER_SCHEMA_URN]: {
          department: "Engineering",
        },
      },
    },
  ]);
  assert.equal(allowed.valid, true);

  const denied = validate([
    {
      op: "replace",
      value: {
        [ENTERPRISE_USER_SCHEMA_URN.toUpperCase()]: {
          department: "Engineering",
          manager: { value: "boss-id" },
        },
      },
    },
  ]);
  assert.equal(denied.valid, false);
  assert.ok(
    denied.violations.some((violation) =>
      violation.attributePath === `${ENTERPRISE_USER_SCHEMA_URN}:manager.value`
    ),
  );
});

test("explicit list of writable leaves is enforced", () => {
  const result = validate(
    [
      { op: "replace", path: "active", value: true },
      {
        op: "replace",
        path: 'emails[type eq "work"].value',
        value: "work@example.com",
      },
    ],
    ["active", 'emails[type eq "work"].value'],
  );

  assert.equal(result.valid, true);

  const denied = validate(
    [
      {
        op: "replace",
        path: 'emails[type eq "work"].value',
        value: "work@example.com",
      },
      {
        op: "replace",
        path: 'emails[type eq "home"].value',
        value: "home@example.com",
      },
    ],
    ['emails[type eq "work"].value'],
  );

  assert.equal(denied.valid, false);
  assert.equal(denied.violations[0]?.reason, "notWritable");
});

test("an explicitly empty writable set denies even readWrite schema attributes", () => {
  const result = validate(
    [{ op: "replace", path: "active", value: true }],
    [],
  );

  assert.equal(result.valid, false);
  assert.equal(result.violations[0]?.reason, "notWritable");
});

test("pathless objects are expanded recursively for schema mutability", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          value: { name: { givenName: "Alice", roles: [{ value: "admin" }] } },
        },
      ],
    },
    {
      schemas,
      writableAttributes: ["name", "name.givenName"],
    },
  );

  assert.equal(result.valid, false);
  assert.ok(
    result.violations.some((violation) =>
      violation.reason === "unknownAttribute" &&
      violation.attributePath === "name.roles"
    ),
  );
});

test("schema readOnly on a filtered leaf cannot be bypassed with casing", () => {
  const readonlyEmailSchema = {
    id: CORE_USER_SCHEMA_URN,
    name: "User",
    attributes: [
      {
        name: "emails",
        type: "complex",
        multiValued: true,
        mutability: "readWrite",
        subAttributes: [
          { name: "value", mutability: "readOnly" },
          { name: "type", mutability: "readWrite" },
        ],
      },
    ],
  };

  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: 'EMAILS[TYPE EQ "WORK"].VALUE',
          value: "work@example.com",
        },
      ],
    },
    {
      schemas: [readonlyEmailSchema],
      writableAttributes: ["emails"],
    },
  );

  assert.equal(result.valid, false);
  assert.equal(result.violations[0]?.reason, "readOnly");
  assert.equal(result.violations[0]?.attributePath, "emails.value");
});

test("a readOnly parent protects the whole subtree even if a child says readWrite", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: 'emails[type eq "work"].value',
          value: "work@example.com",
        },
      ],
    },
    {
      schemas: [
        {
          id: CORE_USER_SCHEMA_URN,
          attributes: [
            {
              name: "emails",
              multiValued: true,
              mutability: "readOnly",
              subAttributes: [
                { name: "value", mutability: "readWrite" },
                { name: "type", mutability: "readWrite" },
              ],
            },
          ],
        },
      ],
      writableAttributes: ["emails"],
    },
  );

  assert.equal(result.valid, false);
  assert.equal(result.violations[0]?.reason, "readOnly");
});

test("immutable schema attributes are rejected", () => {
  const result = validatePatch(
    {
      Operations: [
        { op: "replace", path: "userName", value: "new@example.com" },
      ],
    },
    {
      schemas: [
        {
          id: CORE_USER_SCHEMA_URN,
          attributes: [
            { name: "userName", mutability: "immutable" },
          ],
        },
      ],
      writableAttributes: ["userName"],
    },
  );

  assert.equal(result.valid, false);
  assert.equal(result.violations[0]?.reason, "immutable");
});

test("multiple filter predicates and filtered role paths fail closed", () => {
  const result = validate([
    {
      op: "replace",
      path: 'emails[type eq "work" and value eq "x"].value',
      value: "x",
    },
    {
      op: "replace",
      path: 'roles[type eq "admin"].value',
      value: "admin",
    },
    {
      op: "replace",
      path: 'emails[value eq "a]b"].value',
      value: "x",
    },
  ]);

  assert.equal(result.valid, false);
  assert.ok(result.violations.some((violation) => violation.reason === "invalidFilter"));
  assert.ok(result.violations.some((violation) => violation.reason === "notWritable"));
});

test("nested arrays cannot hide unauthorized attributes from expansion", () => {
  const result = validate([
    {
      op: "replace",
      path: "emails",
      value: [[{ roles: [{ value: "admin" }] }]],
    },
    {
      op: "replace",
      value: { emails: [[{ roles: [{ value: "admin" }] }]] },
    },
  ]);

  assert.equal(result.valid, false);
  assert.ok(result.violations.every((violation) => violation.reason === "invalidValue"));
});

test("malformed filters, pathless remove, and unknown attributes fail closed", () => {
  const result = validate([
    { op: "remove", value: { roles: [{ value: "admin" }] } },
    { op: "replace", path: 'emails[type eq "work"', value: "x" },
    { op: "replace", path: "unknown", value: "x" },
  ]);

  assert.equal(result.valid, false);
  assert.ok(result.violations.some((violation) => violation.reason === "invalidOperation"));
  assert.ok(result.violations.some((violation) => violation.reason === "invalidPath"));
  assert.ok(result.violations.some((violation) => violation.reason === "unknownAttribute"));
});

test("assert helper throws one atomic rejected-batch error", () => {
  assert.throws(
    () =>
      assertPatchAllowed(
        {
          Operations: [
            { op: "replace", path: "active", value: false },
            { op: "replace", path: "roles", value: [{ value: "admin" }] },
          ],
        },
        { schemas, writableAttributes: writable },
      ),
    (error: unknown) => {
      assert.ok(error instanceof ScimPatchValidationError);
      assert.equal(error.status, 403);
      assert.ok(error.violations.length > 0);
      return true;
    },
  );
});

test("schema map input and full core URN paths are supported", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: "USERNAME",
          value: "alice@example.com",
        },
        {
          op: "replace",
          path: `${CORE_USER_SCHEMA_URN.toUpperCase()}:ACTIVE`,
          value: false,
        },
      ],
    },
    {
      schemas: {
        [CORE_USER_SCHEMA_URN]: CORE_USER_SCHEMA,
        [ENTERPRISE_USER_SCHEMA_URN]: ENTERPRISE_USER_SCHEMA,
      },
      writableAttributes: new Set(["userName", "active"]),
    },
  );

  assert.equal(result.valid, true);
});

test("flat attribute schemas and attribute-to-mutability maps are accepted", () => {
  const flat = validatePatch(
    { Operations: [{ op: "replace", path: "active", value: true }] },
    {
      schemas: [{ name: "active", mutability: "readWrite" }],
      writableAttributes: ["active"],
    },
  );
  assert.equal(flat.valid, true);

  const mapped = validatePatch(
    {
      Operations: [
        { op: "replace", path: "active", value: true },
        { op: "replace", path: "roles", value: [] },
      ],
    },
    {
      schemas: {
        active: "readWrite",
        roles: "readOnly",
      },
      writableAttributes: ["active", "roles"],
    },
  );
  assert.equal(mapped.valid, false);
  assert.equal(mapped.violations[0]?.reason, "readOnly");
});

test("standard core URN paths work with an id-less core attribute tree", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: `${CORE_USER_SCHEMA_URN}:active`,
          value: false,
        },
      ],
    },
    {
      schema: {
        attributes: [{ name: "active", mutability: "readWrite" }],
      },
      writableAttributes: ["active"],
    },
  );

  assert.equal(result.valid, true);
});

test("writable authorization also accepts Sets, Maps, and boolean maps", () => {
  const operations = [
    { op: "replace", path: "active", value: true },
    { op: "replace", path: "roles", value: [] },
  ];

  const fromSet = validatePatch(
    { Operations: operations },
    { schemas, writableAttributes: new Set(["active", "roles"]) },
  );
  assert.equal(fromSet.valid, true);

  const fromMap = validatePatch(
    { Operations: operations },
    {
      schemas,
      writableAttributes: new Map([
        ["active", true],
        ["roles", false],
      ]),
    },
  );
  assert.equal(fromMap.valid, false);
  assert.equal(fromMap.violations[0]?.reason, "notWritable");

  const fromRecord = validatePatch(
    { Operations: operations },
    {
      schemas,
      writableAttributes: { active: true, roles: false },
    },
  );
  assert.equal(fromRecord.valid, false);
});

test("flat full-URN attribute lists are grouped into their schemas", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: `${ENTERPRISE_USER_SCHEMA_URN}:department`,
          value: "Engineering",
        },
        { op: "replace", path: "active", value: true },
      ],
    },
    {
      schemas: [
        { name: "active", mutability: "readWrite" },
        {
          name: `${ENTERPRISE_USER_SCHEMA_URN}:department`,
          mutability: "readWrite",
        },
      ],
      writableAttributes: ["active", `${ENTERPRISE_USER_SCHEMA_URN}:department`],
    },
  );

  assert.equal(result.valid, true);
});

test("full-URN names are accepted inside an id-less schema tree", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: `${ENTERPRISE_USER_SCHEMA_URN}:department`,
          value: "Engineering",
        },
      ],
    },
    {
      schemas: {
        attributes: [
          {
            name: `${ENTERPRISE_USER_SCHEMA_URN}:department`,
            mutability: "readWrite",
          },
        ],
      },
      writableAttributes: [`${ENTERPRISE_USER_SCHEMA_URN}:department`],
    },
  );

  assert.equal(result.valid, true);
});

test("case-insensitive duplicate envelope fields fail closed", () => {
  const duplicateOperations = validatePatch(
    {
      Operations: [{ op: "replace", path: "active", value: true }],
      operations: [{ op: "replace", path: "roles", value: [] }],
    },
    { schemas, writableAttributes: writable },
  );
  assert.equal(duplicateOperations.valid, false);
  assert.equal(duplicateOperations.violations[0]?.reason, "invalidPatch");

  const duplicateOperationField = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          OP: "remove",
          path: "active",
          value: true,
        },
      ],
    },
    { schemas, writableAttributes: writable },
  );
  assert.equal(duplicateOperationField.valid, false);
  assert.equal(duplicateOperationField.violations[0]?.reason, "invalidOperation");
});

test("an id-less unique attribute tree can resolve a full extension URN path", () => {
  const result = validatePatch(
    {
      Operations: [
        {
          op: "replace",
          path: `${ENTERPRISE_USER_SCHEMA_URN}:department`,
          value: "Engineering",
        },
      ],
    },
    {
      schema: {
        attributes: [
          { name: "department", mutability: "readWrite" },
        ],
      },
      writableAttributes: ["department"],
    },
  );

  assert.equal(result.valid, true);
});
