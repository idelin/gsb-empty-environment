import {
  createSchemaIndex,
  resolveEntry,
} from "./schema.ts";
import {
  parseScimPath,
  ScimPathError,
} from "./path.ts";
import type {
  AttributeEntry,
  ParsedFilter,
  ParsedScimPath,
  PatchValidationOptions,
  PatchValidationResult,
  PatchViolation,
  PatchViolationReason,
  ResolvedTarget,
  ScimMutability,
  SchemaIndex,
  ScimPatchOperation,
  WritableGrant,
  WritableAttributeInput,
} from "./types.ts";

const VALID_OPERATIONS = new Set(["add", "remove", "replace"]);

interface InvalidContext {
  operationIndex: number;
  op: string | null;
  path: string | null;
  attributePath?: string;
  reason: PatchViolationReason;
  message: string;
  mutability?: ScimMutability;
  status?: number;
}

interface ValidationContext {
  index: SchemaIndex;
  writableProvided: boolean;
  writableGrants: readonly WritableGrant[];
  overrides: ReadonlyMap<string, ScimMutability>;
  violations: PatchViolation[];
  seenViolations: Set<string>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function operationField(
  operation: ScimPatchOperation,
  name: string,
): unknown {
  const direct = operation[name];
  if (direct !== undefined) {
    return direct;
  }
  for (const key of Object.keys(operation)) {
    if (key.toLowerCase() === name) {
      return operation[key];
    }
  }
  return undefined;
}

function operationHasField(
  operation: ScimPatchOperation,
  name: string,
): boolean {
  return Object.keys(operation).some(
    (key) => key.toLowerCase() === name,
  );
}

function operationFieldCount(
  operation: ScimPatchOperation,
  name: string,
): number {
  return Object.keys(operation).filter(
    (key) => key.toLowerCase() === name,
  ).length;
}

function normalizeMutability(value: ScimMutability | string): ScimMutability {
  switch (String(value).trim().toLowerCase()) {
    case "readonly":
      return "readOnly";
    case "readwrite":
      return "readWrite";
    case "immutable":
      return "immutable";
    case "writeonly":
      return "writeOnly";
    default:
      return "readOnly";
  }
}

function collectOverrides(
  options: PatchValidationOptions,
): ReadonlyMap<string, ScimMutability> {
  const overrides = new Map<string, ScimMutability>();

  const addRecords = (
    records: readonly (
      | Readonly<Record<string, ScimMutability | string>>
      | undefined
    )[],
  ): void => {
    for (const record of records) {
      if (!record || typeof record !== "object") {
        continue;
      }
      for (const [path, mutability] of Object.entries(record)) {
        if (typeof mutability !== "string") {
          continue;
        }
        overrides.set(path.trim().toLowerCase(), normalizeMutability(mutability));
      }
    }
  };

  addRecords([
    options.attributeMutability,
    options.mutability,
    options.schemaMutability,
  ]);

  for (const path of options.readOnlyAttributes ?? []) {
    overrides.set(path.trim().toLowerCase(), "readOnly");
  }
  for (const path of options.immutableAttributes ?? []) {
    overrides.set(path.trim().toLowerCase(), "immutable");
  }

  return overrides;
}

function writableInput(
  options: PatchValidationOptions,
): WritableAttributeInput | undefined {
  return (
    options.writableAttributes ??
    options.writablePaths ??
    options.allowedAttributes ??
    options.allowedPaths ??
    options.writable ??
    options.writableAttributeSet ??
    options.callerWritableAttributes
  );
}

function writableValues(
  input: WritableAttributeInput | undefined,
): string[] {
  if (input === undefined) {
    return [];
  }
  if (typeof input === "string") {
    return [input];
  }
  if (input instanceof Map) {
    return [...input.entries()]
      .filter(([, enabled]) => enabled !== false)
      .map(([path]) => path)
      .filter((path): path is string => typeof path === "string");
  }
  if (input instanceof Set) {
    return [...input].filter(
      (path): path is string => typeof path === "string",
    );
  }
  if (Array.isArray(input)) {
    return input.filter(
      (path): path is string => typeof path === "string",
    );
  }
  if (input && typeof input === "object") {
    return Object.entries(input as Readonly<Record<string, boolean>>)
      .filter(([, enabled]) => enabled !== false)
      .map(([path]) => path);
  }
  return [];
}

function schemaUrnFromGrant(
  raw: string,
  index: SchemaIndex,
): string | null {
  const lowered = raw.trim().toLowerCase();
  return index.knownSchemaUrns.find((urn) => urn === lowered) ?? null;
}

function buildWritableGrants(
  input: WritableAttributeInput | undefined,
  index: SchemaIndex,
): WritableGrant[] {
  const grants: WritableGrant[] = [];

  for (const raw of writableValues(input)) {
    if (typeof raw !== "string" || !raw.trim()) {
      continue;
    }

    const schemaWide = schemaUrnFromGrant(raw, index);
    if (schemaWide !== null) {
      grants.push({
        schemaId: schemaWide,
        segments: [],
        allSchema: true,
      });
      continue;
    }

    try {
      const parsed = parseScimPath(raw, index.knownSchemaUrns);
      const entry = resolveEntry(
        parsed.schemaUrn ?? null,
        parsed.targetSegments,
        index,
      );
      if (!entry) {
        continue;
      }
      grants.push({
        schemaId: entry.schemaId,
        segments: entry.segments,
        filter: parsed.filter,
        allSchema: false,
      });
    } catch {
      // A malformed grant must not become a wildcard or an implicit allow.
    }
  }

  return grants;
}

function effectiveMutability(
  entry: AttributeEntry,
  overrides: ReadonlyMap<string, ScimMutability>,
): ScimMutability {
  return (
    overrides.get(entry.fullKey.toLowerCase()) ??
    overrides.get(entry.bareKey.toLowerCase()) ??
    entry.effectiveMutability
  );
}

function filterValueEqual(left: unknown, right: unknown): boolean {
  if (typeof left === "string" && typeof right === "string") {
    return left.toLowerCase() === right.toLowerCase();
  }
  return left === right;
}

function filterEqual(
  left: ParsedFilter | undefined,
  right: ParsedFilter | undefined,
): boolean {
  if (!left && !right) {
    return true;
  }
  if (!left || !right) {
    return false;
  }
  return (
    left.attributeSegments.join(".") === right.attributeSegments.join(".") &&
    left.op === right.op &&
    filterValueEqual(left.value, right.value)
  );
}

function isPathPrefix(
  prefix: readonly string[],
  target: readonly string[],
): boolean {
  if (prefix.length > target.length) {
    return false;
  }
  for (let index = 0; index < prefix.length; index += 1) {
    if (prefix[index] !== target[index]) {
      return false;
    }
  }
  return true;
}

function grantAllows(
  grant: WritableGrant,
  entry: AttributeEntry,
  filter: ParsedFilter | undefined,
): boolean {
  if (grant.allSchema) {
    return grant.schemaId === entry.schemaId;
  }

  if (grant.schemaId !== entry.schemaId) {
    return false;
  }
  if (!isPathPrefix(grant.segments, entry.segments)) {
    return false;
  }
  if (grant.filter && !filterEqual(grant.filter, filter)) {
    return false;
  }
  return true;
}

function attributePathFor(entry: AttributeEntry): string {
  if (entry.schemaIsCore) {
    return entry.bareKey;
  }
  return `${entry.schemaDisplayId ?? entry.schemaId}:${entry.bareKey}`;
}

function addViolation(
  context: ValidationContext,
  violation: InvalidContext,
): void {
  const attributePath = violation.attributePath ?? "<patch>";
  const key = [
    violation.operationIndex,
    violation.reason,
    attributePath,
    violation.path ?? "",
  ].join("|");

  if (context.seenViolations.has(key)) {
    return;
  }
  context.seenViolations.add(key);

  const result: PatchViolation = {
    operationIndex: violation.operationIndex,
    op: violation.op,
    path: violation.path,
    attributePath,
    reason: violation.reason,
    message: violation.message,
    status: violation.status ??
      (violation.reason === "invalidOperation" ||
          violation.reason === "invalidPath" ||
          violation.reason === "invalidFilter" ||
          violation.reason === "invalidValue" ||
          violation.reason === "invalidPatch"
        ? 400
        : 403),
  };
  if (violation.mutability !== undefined) {
    result.mutability = violation.mutability;
  }
  context.violations.push(result);
}

function checkTarget(
  target: ResolvedTarget,
  rawPath: string | null,
  context: ValidationContext,
  operationIndex: number,
  op: string | null,
): void {
  const mutability = effectiveMutability(target.entry, context.overrides);

  if (mutability === "readOnly") {
    addViolation(context, {
      operationIndex,
      op,
      path: rawPath,
      attributePath: target.attributePath,
      reason: "readOnly",
      mutability,
      message: `Attribute "${target.attributePath}" is read-only and cannot be changed by PATCH`,
    });
    return;
  }

  if (mutability === "immutable") {
    addViolation(context, {
      operationIndex,
      op,
      path: rawPath,
      attributePath: target.attributePath,
      reason: "immutable",
      mutability,
      message: `Attribute "${target.attributePath}" is immutable and cannot be changed by PATCH`,
    });
    return;
  }

  if (
    context.writableProvided &&
    !context.writableGrants.some((grant) =>
      grantAllows(grant, target.entry, target.filter)
    )
  ) {
    addViolation(context, {
      operationIndex,
      op,
      path: rawPath,
      attributePath: target.attributePath,
      reason: "notWritable",
      mutability,
      message: `Attribute "${target.attributePath}" is outside the caller's writable attribute set`,
    });
  }
}

function validateFilterAgainstSchema(
  parsed: ParsedScimPath,
  baseEntry: AttributeEntry,
  context: ValidationContext,
  operationIndex: number,
  op: string | null,
): boolean {
  if (!parsed.filter) {
    return true;
  }

  const filterSegments = [
    ...baseEntry.segments,
    ...parsed.filter.attributeSegments,
  ];
  const filterEntry = resolveEntry(
    baseEntry.schemaId,
    filterSegments,
    context.index,
  );

  if (!filterEntry) {
    addViolation(context, {
      operationIndex,
      op,
      path: parsed.raw,
      attributePath: parsed.filter.attribute,
      reason: "invalidFilter",
      message: `Filter attribute "${parsed.filter.attribute}" is not defined for "${attributePathFor(baseEntry)}"`,
    });
    return false;
  }

  return true;
}

function resolveParsedTarget(
  parsed: ParsedScimPath,
  context: ValidationContext,
  operationIndex: number,
  op: string | null,
): ResolvedTarget | null {
  const schemaId = parsed.schemaUrn ?? null;
  const baseEntry = resolveEntry(
    schemaId,
    parsed.attributeSegments,
    context.index,
  );

  if (!baseEntry) {
    const display = schemaId
      ? `${schemaId}:${parsed.attributeSegments.join(".")}`
      : parsed.attributeSegments.join(".");
    addViolation(context, {
      operationIndex,
      op,
      path: parsed.raw,
      attributePath: display,
      reason: "unknownAttribute",
      message: `PATCH path attribute "${display}" is not defined in the supplied schema`,
    });
    return null;
  }

  if (!validateFilterAgainstSchema(parsed, baseEntry, context, operationIndex, op)) {
    return null;
  }

  const targetEntry = parsed.subAttributeSegments.length > 0
    ? resolveEntry(schemaId, parsed.targetSegments, context.index)
    : baseEntry;

  if (!targetEntry) {
    const display = schemaId
      ? `${schemaId}:${parsed.targetSegments.join(".")}`
      : parsed.targetSegments.join(".");
    addViolation(context, {
      operationIndex,
      op,
      path: parsed.raw,
      attributePath: display,
      reason: "unknownAttribute",
      message: `PATCH path sub-attribute "${display}" is not defined in the supplied schema`,
    });
    return null;
  }

  return {
    entry: targetEntry,
    filter: parsed.filter,
    attributePath: attributePathFor(targetEntry),
  };
}

function relativeSegments(
  key: string,
): string[] | null {
  if (!key || key.includes(":") || key.includes("[") || key.includes("]")) {
    return null;
  }
  const segments = key.split(".");
  if (segments.some((segment) => !segment)) {
    return null;
  }
  return segments.map((segment) => segment.toLowerCase());
}

function expandRelative(
  value: unknown,
  target: ResolvedTarget,
  rawPath: string | null,
  context: ValidationContext,
  operationIndex: number,
  op: string | null,
): void {
  if (value === null || typeof value !== "object") {
    checkTarget(target, rawPath, context, operationIndex, op);
    return;
  }

  if (Array.isArray(value)) {
    if (value.length === 0) {
      checkTarget(target, rawPath, context, operationIndex, op);
      return;
    }

    for (const item of value) {
      if (Array.isArray(item)) {
        addViolation(context, {
          operationIndex,
          op,
          path: rawPath,
          attributePath: target.attributePath,
          reason: "invalidValue",
          message: `Nested arrays are not valid for attribute "${target.attributePath}"`,
        });
      } else if (isRecord(item)) {
        expandRelativeObject(
          item,
          target,
          rawPath,
          context,
          operationIndex,
          op,
        );
      } else {
        checkTarget(target, rawPath, context, operationIndex, op);
      }
    }
    return;
  }

  const keys = Object.keys(value);
  if (keys.length === 0) {
    checkTarget(target, rawPath, context, operationIndex, op);
    return;
  }

  expandRelativeObject(
    value as Record<string, unknown>,
    target,
    rawPath,
    context,
    operationIndex,
    op,
  );
}

function expandRelativeObject(
  value: Record<string, unknown>,
  target: ResolvedTarget,
  rawPath: string | null,
  context: ValidationContext,
  operationIndex: number,
  op: string | null,
): void {
  const keys = Object.keys(value);
  if (keys.length === 0) {
    checkTarget(target, rawPath, context, operationIndex, op);
    return;
  }

  for (const key of keys) {
    const relative = relativeSegments(key);
    if (!relative) {
      addViolation(context, {
        operationIndex,
        op,
        path: rawPath,
        attributePath: `${target.attributePath}.${key}`,
        reason: "unknownAttribute",
        message: `Nested attribute "${key}" is not valid below "${target.attributePath}"`,
      });
      continue;
    }

    const childEntry = resolveEntry(
      target.entry.schemaId,
      [...target.entry.segments, ...relative],
      context.index,
    );
    if (!childEntry) {
      addViolation(context, {
        operationIndex,
        op,
        path: rawPath,
        attributePath: `${attributePathFor(target.entry)}.${relative.join(".")}`,
        reason: "unknownAttribute",
        message: `Nested attribute "${relative.join(".")}" is not defined below "${attributePathFor(target.entry)}"`,
      });
      continue;
    }

    expandRelative(
      value[key],
      {
        entry: childEntry,
        filter: target.filter,
        attributePath: attributePathFor(childEntry),
      },
      rawPath,
      context,
      operationIndex,
      op,
    );
  }
}

function expandNoPathValue(
  value: Record<string, unknown>,
  context: ValidationContext,
  operationIndex: number,
  op: string | null,
): void {
  for (const key of Object.keys(value)) {
    const schemaId = schemaUrnFromGrant(key, context.index);

    if (schemaId !== null) {
      const extensionValue = value[key];
      if (!isRecord(extensionValue)) {
        addViolation(context, {
          operationIndex,
          op,
          path: null,
          attributePath: schemaId,
          reason: "invalidValue",
          message: `Schema extension key "${key}" must contain an object of attributes`,
        });
        continue;
      }

      for (const childKey of Object.keys(extensionValue)) {
        const relative = relativeSegments(childKey);
        if (!relative) {
          addViolation(context, {
            operationIndex,
            op,
            path: null,
            attributePath: `${schemaId}:${childKey}`,
            reason: "unknownAttribute",
            message: `Attribute "${childKey}" is not valid in schema "${schemaId}"`,
          });
          continue;
        }

        const entry = resolveEntry(schemaId, relative, context.index);
        if (!entry) {
          addViolation(context, {
            operationIndex,
            op,
            path: null,
            attributePath: `${schemaId}:${relative.join(".")}`,
            reason: "unknownAttribute",
            message: `Attribute "${relative.join(".")}" is not defined in schema "${schemaId}"`,
          });
          continue;
        }

        expandRelative(
          extensionValue[childKey],
          {
            entry,
            attributePath: attributePathFor(entry),
          },
          null,
          context,
          operationIndex,
          op,
        );
      }
      continue;
    }

    let parsed: ParsedScimPath;
    try {
      parsed = parseScimPath(key, context.index.knownSchemaUrns);
    } catch (error) {
      addViolation(context, {
        operationIndex,
        op,
        path: null,
        attributePath: key,
        reason:
          error instanceof ScimPathError && error.code === "INVALID_FILTER"
            ? "invalidFilter"
            : "invalidPath",
        message: `Invalid attribute key "${key}" in pathless PATCH value`,
      });
      continue;
    }

    if (parsed.filter) {
      addViolation(context, {
        operationIndex,
        op,
        path: null,
        attributePath: parsed.raw,
        reason: "invalidPath",
        message: "Value filters are only valid in PATCH paths, not object keys",
      });
      continue;
    }

    const target = resolveParsedTarget(parsed, context, operationIndex, op);
    if (!target) {
      continue;
    }
    expandRelative(
      value[key],
      target,
      null,
      context,
      operationIndex,
      op,
    );
  }
}

function validateOperation(
  rawOperation: unknown,
  operationIndex: number,
  context: ValidationContext,
): void {
  if (!isRecord(rawOperation)) {
    addViolation(context, {
      operationIndex,
      op: null,
      path: null,
      reason: "invalidOperation",
      message: "Each PATCH operation must be a JSON object",
    });
    return;
  }

  const operation = rawOperation as ScimPatchOperation;
  const duplicateField = ["op", "path", "value"].find(
    (name) => operationFieldCount(operation, name) > 1,
  );
  if (duplicateField) {
    addViolation(context, {
      operationIndex,
      op: null,
      path: null,
      reason: "invalidOperation",
      message: `PATCH operation contains duplicate case-insensitive "${duplicateField}" fields`,
    });
    return;
  }

  const rawOp = operationField(operation, "op");
  if (typeof rawOp !== "string" || !VALID_OPERATIONS.has(rawOp.toLowerCase())) {
    addViolation(context, {
      operationIndex,
      op: typeof rawOp === "string" ? rawOp : null,
      path: null,
      reason: "invalidOperation",
      message: "PATCH operation must be add, remove, or replace",
    });
    return;
  }

  const op = rawOp.toLowerCase();
  const rawPathValue = operationHasField(operation, "path")
    ? operationField(operation, "path")
    : undefined;

  if (rawPathValue !== undefined && typeof rawPathValue !== "string") {
    addViolation(context, {
      operationIndex,
      op,
      path: null,
      reason: "invalidPath",
      message: "PATCH path must be a string",
    });
    return;
  }

  const rawPath = typeof rawPathValue === "string" ? rawPathValue : null;
  const hasPath = rawPath !== null;
  const isEmptyPath = rawPath !== null && rawPath.trim() === "";

  if (isEmptyPath) {
    addViolation(context, {
      operationIndex,
      op,
      path: rawPath,
      reason: "invalidPath",
      message: "PATCH path must not be empty",
    });
    return;
  }
  if (op === "remove" && !hasPath) {
    addViolation(context, {
      operationIndex,
      op,
      path: null,
      reason: "invalidOperation",
      message: "A remove operation requires a path",
    });
    return;
  }

  const hasValue = operationHasField(operation, "value");
  const value = operationField(operation, "value");

  if (op !== "remove" && !hasValue) {
    addViolation(context, {
      operationIndex,
      op,
      path: rawPath,
      reason: "invalidValue",
      message: `A ${op} operation requires a value`,
    });
    return;
  }

  if (!hasPath) {
    if (!isRecord(value)) {
      addViolation(context, {
        operationIndex,
        op,
        path: null,
        reason: "invalidValue",
        message: `A pathless ${op} operation requires an object value`,
      });
      return;
    }
    expandNoPathValue(
      value as Record<string, unknown>,
      context,
      operationIndex,
      op,
    );
    return;
  }

  let parsed: ParsedScimPath;
  try {
    parsed = parseScimPath(rawPath ?? "", context.index.knownSchemaUrns);
  } catch (error) {
    addViolation(context, {
      operationIndex,
      op,
      path: rawPath,
      reason:
        error instanceof ScimPathError && error.code === "INVALID_FILTER"
          ? "invalidFilter"
          : "invalidPath",
      message: error instanceof Error ? error.message : "Invalid PATCH path",
    });
    return;
  }

  const target = resolveParsedTarget(parsed, context, operationIndex, op);
  if (!target) {
    return;
  }

  if (op === "remove") {
    checkTarget(target, rawPath, context, operationIndex, op);
    return;
  }

  expandRelative(value, target, rawPath, context, operationIndex, op);
}

function extractOperations(
  patch: unknown,
): { operations: readonly unknown[] | null; error?: string } {
  if (Array.isArray(patch)) {
    return { operations: patch };
  }
  if (!isRecord(patch)) {
    return { operations: null };
  }

  let operationsKey: string | null = null;
  for (const key of Object.keys(patch)) {
    if (key.toLowerCase() === "operations") {
      if (operationsKey !== null) {
        return {
          operations: null,
          error: "PATCH body contains duplicate case-insensitive Operations fields",
        };
      }
      operationsKey = key;
    }
  }

  if (operationsKey !== null) {
    const operations = patch[operationsKey];
    return Array.isArray(operations)
      ? { operations }
      : { operations: null, error: "PATCH Operations field must be an array" };
  }

  if (operationHasField(patch as ScimPatchOperation, "op")) {
    return { operations: [patch] };
  }

  return { operations: null };
}

export function validatePatch(
  patch: unknown,
  options: PatchValidationOptions,
): PatchValidationResult {
  if (!options || typeof options !== "object") {
    throw new TypeError("validatePatch requires a schema/policy options object");
  }

  const index = createSchemaIndex(options.schemas ?? options.schema);
  const writable = writableInput(options);
  const context: ValidationContext = {
    index,
    writableProvided: writable !== undefined,
    writableGrants: buildWritableGrants(writable, index),
    overrides: collectOverrides(options),
    violations: [],
    seenViolations: new Set<string>(),
  };

  const extracted = extractOperations(patch);
  if (!extracted.operations) {
    addViolation(context, {
      operationIndex: -1,
      op: null,
      path: null,
      reason: "invalidPatch",
      message:
        extracted.error ?? "PATCH body must contain an Operations array",
    });
  } else {
    extracted.operations.forEach((operation, operationIndex) => {
      validateOperation(operation, operationIndex, context);
    });
  }

  const valid = context.violations.length === 0;
  return {
    valid,
    ok: valid,
    allowed: valid,
    violations: context.violations,
    errors: context.violations,
  };
}

export class ScimPatchValidationError extends Error {
  readonly code = "SCIM_PATCH_REJECTED";
  readonly status: number;
  readonly violations: readonly PatchViolation[];

  constructor(
    resultOrViolations:
      | PatchValidationResult
      | readonly PatchViolation[],
  ) {
    const violations = Array.isArray(resultOrViolations)
      ? resultOrViolations
      : resultOrViolations.violations;
    const message = violations.length > 0
      ? `SCIM PATCH rejected: ${violations
          .map((violation) => violation.message)
          .join("; ")}`
      : "SCIM PATCH rejected";

    super(message);
    this.name = "ScimPatchValidationError";
    this.violations = violations;
    this.status = violations.some((violation) => violation.status === 400)
      ? 400
      : 403;
  }
}

export function assertPatchAllowed(
  patch: unknown,
  options: PatchValidationOptions,
): PatchValidationResult {
  const result = validatePatch(patch, options);
  if (!result.valid) {
    throw new ScimPatchValidationError(result);
  }
  return result;
}

export function isPatchAllowed(
  patch: unknown,
  options: PatchValidationOptions,
): boolean {
  return validatePatch(patch, options).valid;
}

export const validateScimPatch = validatePatch;
export const validatePatchOperations = validatePatch;
export const checkPatch = validatePatch;
export const assertValidPatch = assertPatchAllowed;
export const validatePatchOrThrow = assertPatchAllowed;
export const assertPatchAuthorized = assertPatchAllowed;
export { ScimPatchValidationError as ScimPatchError };

export function validatePatchOperation(
  operation: unknown,
  options: PatchValidationOptions,
): PatchValidationResult {
  return validatePatch({ Operations: [operation] }, options);
}

export function isPatchAuthorized(
  patch: unknown,
  options: PatchValidationOptions,
): boolean {
  return validatePatch(patch, options).valid;
}
