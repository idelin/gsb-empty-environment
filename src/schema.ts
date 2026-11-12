import type {
  AttributeEntry,
  ScimAttributeSchema,
  ScimMutability,
  ScimSchema,
  ScimSchemaInput,
  ScimSchemaMapValue,
  SchemaIndex,
} from "./types.ts";

export const CORE_USER_SCHEMA_URN =
  "urn:ietf:params:scim:schemas:core:2.0:User";

export const ENTERPRISE_USER_SCHEMA_URN =
  "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User";

const emailSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "value", mutability: "readWrite" },
  { name: "display", mutability: "readWrite" },
  { name: "type", mutability: "readWrite" },
  { name: "primary", type: "boolean", mutability: "readWrite" },
];

const phoneSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "value", mutability: "readWrite" },
  { name: "display", mutability: "readWrite" },
  { name: "type", mutability: "readWrite" },
  { name: "primary", type: "boolean", mutability: "readWrite" },
];

const nameSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "formatted", mutability: "readWrite" },
  { name: "familyName", mutability: "readWrite" },
  { name: "givenName", mutability: "readWrite" },
  { name: "middleName", mutability: "readWrite" },
  { name: "honorificPrefix", mutability: "readWrite" },
  { name: "honorificSuffix", mutability: "readWrite" },
];

const multiValueTypeSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "value", mutability: "readWrite" },
  { name: "display", mutability: "readWrite" },
  { name: "type", mutability: "readWrite" },
  { name: "primary", type: "boolean", mutability: "readWrite" },
];

const groupsSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "value", mutability: "readOnly" },
  { name: "$ref", mutability: "readOnly" },
  { name: "display", mutability: "readOnly" },
  { name: "type", mutability: "readOnly" },
];

const metaSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "resourceType", mutability: "readOnly" },
  { name: "created", type: "dateTime", mutability: "readOnly" },
  { name: "lastModified", type: "dateTime", mutability: "readOnly" },
  { name: "location", mutability: "readOnly" },
  { name: "version", mutability: "readOnly" },
];

const managerSubAttributes: readonly ScimAttributeSchema[] = [
  { name: "value", mutability: "readWrite" },
  { name: "$ref", mutability: "readWrite" },
  { name: "displayName", mutability: "readWrite" },
];

export const CORE_USER_SCHEMA: ScimSchema = {
  id: CORE_USER_SCHEMA_URN,
  name: "User",
  attributes: [
    { name: "id", mutability: "readOnly" },
    { name: "externalId", mutability: "readWrite" },
    { name: "meta", mutability: "readOnly", subAttributes: metaSubAttributes },
    { name: "schemas", type: "string", multiValued: true, mutability: "readOnly" },
    { name: "userName", mutability: "readWrite" },
    { name: "name", mutability: "readWrite", subAttributes: nameSubAttributes },
    { name: "displayName", mutability: "readWrite" },
    { name: "nickName", mutability: "readWrite" },
    { name: "profileUrl", mutability: "readWrite" },
    { name: "title", mutability: "readWrite" },
    { name: "userType", mutability: "readWrite" },
    { name: "preferredLanguage", mutability: "readWrite" },
    { name: "locale", mutability: "readWrite" },
    { name: "timezone", mutability: "readWrite" },
    { name: "active", type: "boolean", mutability: "readWrite" },
    { name: "password", mutability: "writeOnly" },
    {
      name: "emails",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: emailSubAttributes,
    },
    {
      name: "phoneNumbers",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: phoneSubAttributes,
    },
    {
      name: "ims",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: multiValueTypeSubAttributes,
    },
    {
      name: "photos",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: multiValueTypeSubAttributes,
    },
    {
      name: "addresses",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: [
        { name: "formatted", mutability: "readWrite" },
        { name: "streetAddress", mutability: "readWrite" },
        { name: "locality", mutability: "readWrite" },
        { name: "region", mutability: "readWrite" },
        { name: "postalCode", mutability: "readWrite" },
        { name: "country", mutability: "readWrite" },
        { name: "type", mutability: "readWrite" },
        { name: "primary", type: "boolean", mutability: "readWrite" },
      ],
    },
    {
      name: "groups",
      type: "complex",
      multiValued: true,
      mutability: "readOnly",
      subAttributes: groupsSubAttributes,
    },
    {
      name: "entitlements",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: multiValueTypeSubAttributes,
    },
    {
      name: "roles",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: multiValueTypeSubAttributes,
    },
    {
      name: "x509Certificates",
      type: "complex",
      multiValued: true,
      mutability: "readWrite",
      subAttributes: [
        { name: "value", mutability: "readWrite" },
        { name: "display", mutability: "readWrite" },
        { name: "type", mutability: "readWrite" },
        { name: "primary", type: "boolean", mutability: "readWrite" },
      ],
    },
  ],
};

export const ENTERPRISE_USER_SCHEMA: ScimSchema = {
  id: ENTERPRISE_USER_SCHEMA_URN,
  name: "EnterpriseUser",
  attributes: [
    { name: "employeeNumber", mutability: "readWrite" },
    { name: "costCenter", mutability: "readWrite" },
    { name: "organization", mutability: "readWrite" },
    { name: "division", mutability: "readWrite" },
    { name: "department", mutability: "readWrite" },
    {
      name: "manager",
      type: "complex",
      mutability: "readWrite",
      subAttributes: managerSubAttributes,
    },
  ],
};

export const ENTERPRISE_EXTENSION_SCHEMA = ENTERPRISE_USER_SCHEMA;
export const CORE_SCHEMA = CORE_USER_SCHEMA;
export const USER_SCHEMA = CORE_USER_SCHEMA;
export const ENTERPRISE_SCHEMA = ENTERPRISE_USER_SCHEMA;

export const DEFAULT_USER_SCHEMAS: readonly ScimSchema[] = [
  CORE_USER_SCHEMA,
  ENTERPRISE_USER_SCHEMA,
];

export const USER_SCHEMAS = DEFAULT_USER_SCHEMAS;

function normalizeMutability(
  value: ScimMutability | string | undefined,
  inherited: ScimMutability,
): ScimMutability {
  const own = value === undefined
    ? inherited
    : (() => {
        switch (String(value).trim().toLowerCase()) {
          case "readonly":
            return "readOnly" as const;
          case "readwrite":
            return "readWrite" as const;
          case "immutable":
            return "immutable" as const;
          case "writeonly":
            return "writeOnly" as const;
          default:
            // Unknown schema mutability fails closed.
            return "readOnly" as const;
        }
      })();

  // readOnly and immutable parents protect their entire subtree. A child can
  // make a writable parent stricter, but cannot relax the parent's protection.
  if (inherited === "readOnly") {
    return "readOnly";
  }
  if (inherited === "immutable" && own !== "readOnly") {
    return "immutable";
  }
  return own;
}

function schemaIdOf(schema: ScimSchema): string | undefined {
  for (const key of ["id", "schema", "schemaId", "urn", "schemaUrn"] as const) {
    const value = schema[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }
  return undefined;
}

function isCoreSchemaId(schemaId: string | null): boolean {
  if (schemaId === null) {
    return true;
  }
  return schemaId.includes(":schemas:core:");
}

function appearsToBeSchema(value: unknown): value is ScimSchema {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const record = value as Record<string, unknown>;
  return Array.isArray(record.attributes) || "attributes" in record;
}

function appearsToBeAttribute(value: unknown): value is ScimAttributeSchema {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const record = value as Record<string, unknown>;
  return typeof record.name === "string" && !("attributes" in record);
}

function groupFlatAttributes(
  attributes: readonly ScimAttributeSchema[],
): ScimSchema[] {
  const coreAttributes: ScimAttributeSchema[] = [];
  const extensionAttributes = new Map<string, {
    displayId: string;
    attributes: ScimAttributeSchema[];
  }>();

  for (const attribute of attributes) {
    const name = attribute.name;
    if (!name.toLowerCase().startsWith("urn:")) {
      coreAttributes.push(attribute);
      continue;
    }

    const delimiter = name.lastIndexOf(":");
    if (delimiter <= "urn:".length || delimiter === name.length - 1) {
      coreAttributes.push(attribute);
      continue;
    }

    const displayId = name.slice(0, delimiter);
    const attributeName = name.slice(delimiter + 1);
    const key = displayId.toLowerCase();
    const group = extensionAttributes.get(key) ?? {
      displayId,
      attributes: [],
    };
    group.attributes.push({ ...attribute, name: attributeName });
    extensionAttributes.set(key, group);
  }

  const schemas: ScimSchema[] = [];
  if (coreAttributes.length > 0) {
    schemas.push({ attributes: coreAttributes });
  }
  for (const group of extensionAttributes.values()) {
    schemas.push({ id: group.displayId, attributes: group.attributes });
  }
  return schemas;
}

function normalizeSchemaInput(input: ScimSchemaInput | undefined): ScimSchema[] {
  if (input === undefined || input === null) {
    return [];
  }

  if (Array.isArray(input)) {
    const items = [...input];
    if (items.every((item) => appearsToBeAttribute(item))) {
      return groupFlatAttributes(items as ScimAttributeSchema[]);
    }
    return items.filter(
      (item) => item !== null && typeof item === "object" && !Array.isArray(item),
    ) as ScimSchema[];
  }

  if (!input || typeof input !== "object") {
    return [];
  }

  const record = input as Record<string, unknown>;
  if (Array.isArray(record.schemas)) {
    return (record.schemas as ScimSchema[]).slice();
  }

  if (appearsToBeSchema(record)) {
    return [record as ScimSchema];
  }
  if (appearsToBeAttribute(record)) {
    return groupFlatAttributes([record as ScimAttributeSchema]);
  }

  const entries = Object.entries(
    input as Record<string, ScimSchemaMapValue>,
  );
  if (
    entries.length > 0 &&
    entries.every(([, value]) => typeof value === "string")
  ) {
    return groupFlatAttributes(entries.map(([name, mutability]) => ({
        name,
        mutability: mutability as string,
      })));
  }
  if (
    entries.length > 0 &&
    entries.every(([, value]) => appearsToBeAttribute(value))
  ) {
    return groupFlatAttributes(entries.map(([name, value]) => ({
        ...(value as ScimAttributeSchema),
        name: (value as ScimAttributeSchema).name || name,
      })));
  }

  const schemas: ScimSchema[] = [];
  for (const [schemaId, value] of entries) {
    if (Array.isArray(value)) {
      schemas.push({ id: schemaId, attributes: value });
    } else if (appearsToBeSchema(value)) {
      const schema = value as ScimSchema;
      schemas.push({
        ...schema,
        id: schemaIdOf(schema) ?? schemaId,
      });
    }
  }
  return schemas;
}

function attributeNameSegments(name: string): string[] {
  return name
    .split(".")
    .filter(Boolean)
    .map((segment) => segment.toLowerCase());
}

export function createSchemaIndex(
  input?: ScimSchemaInput,
): SchemaIndex {
  const schemas = normalizeSchemaInput(input);
  const entries: AttributeEntry[] = [];
  const byFull = new Map<string, AttributeEntry>();
  const byBare = new Map<string, AttributeEntry[]>();
  const knownSchemaUrns: string[] = [];

  for (const schema of schemas) {
    const schemaDisplayId = schemaIdOf(schema) ?? null;
    const schemaId = schemaDisplayId?.toLowerCase() ?? null;

    if (schemaId !== null && !knownSchemaUrns.includes(schemaId)) {
      knownSchemaUrns.push(schemaId);
    }

    const walk = (
      attributes: readonly ScimAttributeSchema[],
      parentSegments: readonly string[],
      inheritedMutability: ScimMutability,
      activeSchemaId: string | null,
      activeSchemaDisplayId: string | null,
    ): void => {
      for (const attribute of attributes) {
        if (
          !attribute ||
          typeof attribute !== "object" ||
          typeof attribute.name !== "string"
        ) {
          continue;
        }
        let attributeName = attribute.name;
        let entrySchemaId = activeSchemaId;
        let entrySchemaDisplayId = activeSchemaDisplayId;
        const loweredName = attributeName.toLowerCase();

        if (loweredName.startsWith("urn:")) {
          const activePrefix = activeSchemaDisplayId
            ? `${activeSchemaDisplayId.toLowerCase()}:`
            : activeSchemaId
              ? `${activeSchemaId}:`
              : null;

          if (activePrefix && loweredName.startsWith(activePrefix)) {
            attributeName = attributeName.slice(activePrefix.length);
          } else {
            const delimiter = attributeName.lastIndexOf(":");
            if (delimiter > "urn:".length && delimiter < attributeName.length - 1) {
              entrySchemaDisplayId = attributeName.slice(0, delimiter);
              entrySchemaId = entrySchemaDisplayId.toLowerCase();
              attributeName = attributeName.slice(delimiter + 1);
              if (!knownSchemaUrns.includes(entrySchemaId)) {
                knownSchemaUrns.push(entrySchemaId);
              }
            }
          }
        }

        const nameSegments = attributeNameSegments(attributeName);
        if (nameSegments.length === 0) {
          continue;
        }

        const segments = [...parentSegments, ...nameSegments];
        const fullKey = entrySchemaId === null
          ? segments.join(".")
          : `${entrySchemaId}:${segments.join(".")}`;
        const bareKey = segments.join(".");
        const effectiveMutability = normalizeMutability(
          attribute.mutability,
          inheritedMutability,
        );

        const entry: AttributeEntry = {
          schemaId: entrySchemaId,
          schemaDisplayId: entrySchemaDisplayId,
          schemaIsCore: isCoreSchemaId(entrySchemaId),
          segments,
          attribute,
          effectiveMutability,
          fullKey,
          bareKey,
        };

        entries.push(entry);
        byFull.set(fullKey, entry);

        const bareEntries = byBare.get(bareKey);
        if (bareEntries) {
          bareEntries.push(entry);
        } else {
          byBare.set(bareKey, [entry]);
        }

        if (
          Array.isArray(attribute.subAttributes) &&
          attribute.subAttributes.length > 0
        ) {
          walk(
            attribute.subAttributes,
            segments,
            effectiveMutability,
            entrySchemaId,
            entrySchemaDisplayId,
          );
        }
      }
    };

    walk(
      Array.isArray(schema.attributes) ? schema.attributes : [],
      [],
      "readWrite",
      schemaId,
      schemaDisplayId,
    );
  }

  return {
    entries,
    byFull,
    byBare,
    knownSchemaUrns,
  };
}

export function resolveEntry(
  schemaId: string | null,
  segments: readonly string[],
  index: SchemaIndex,
): AttributeEntry | null {
  const key = segments.join(".");
  if (schemaId !== null) {
    const fullEntry = index.byFull.get(`${schemaId.toLowerCase()}:${key}`);
    if (fullEntry) {
      return fullEntry;
    }
    // A caller may provide only the core attribute tree (no schema id). In
    // that case still accept the standard core URN path form, but never use
    // the fallback for extension URNs.
    if (schemaId.toLowerCase().includes(":schemas:core:")) {
      return index.byBare.get(key)?.find((entry) => entry.schemaIsCore) ?? null;
    }
    if (index.knownSchemaUrns.length === 0) {
      const candidates = index.byBare.get(key);
      return candidates && candidates.length === 1 ? candidates[0] : null;
    }
    return null;
  }

  const candidates = index.byBare.get(key);
  if (!candidates || candidates.length === 0) {
    return null;
  }

  const coreCandidate = candidates.find((entry) => entry.schemaIsCore);
  return coreCandidate ?? (candidates.length === 1 ? candidates[0] : null);
}
