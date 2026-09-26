import {
  deepEqual,
  isPlainObject,
  joinPointer,
  type Issue,
  type JsonValue,
  type Schema,
} from "./schema.ts";

function escape(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

function typeOf(v: JsonValue): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  if (typeof v === "number" && Number.isInteger(v)) return "integer";
  return typeof v;
}

export function validate(
  schema: Schema,
  value: JsonValue,
  instancePath: string,
  schemaPath: string,
  issues: Issue[],
): void {
  const fail = (keyword: string, message: string, suffix = ""): void => {
    issues.push({ instancePath, schemaPath: schemaPath + suffix, keyword, message });
  };

  if (schema.type !== undefined) {
    const actual = typeOf(value);
    const matches =
      schema.type === actual || (schema.type === "number" && actual === "integer");
    if (!matches) {
      fail("type", `expected type "${schema.type}", got "${actual}"`, "/type");
      return; // further keyword checks are meaningless on a wrong type
    }
  }

  if (schema.const !== undefined && !deepEqual(value, schema.const)) {
    fail("const", `expected constant ${JSON.stringify(schema.const)}`, "/const");
  }
  if (schema.enum !== undefined && !schema.enum.some((e) => deepEqual(e, value))) {
    fail("enum", `value not in enum ${JSON.stringify(schema.enum)}`, "/enum");
  }

  if (typeof value === "string") {
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      fail("minLength", `string shorter than minLength ${schema.minLength}`, "/minLength");
    }
    if (schema.maxLength !== undefined && value.length > schema.maxLength) {
      fail("maxLength", `string longer than maxLength ${schema.maxLength}`, "/maxLength");
    }
    if (schema.pattern !== undefined && !new RegExp(schema.pattern).test(value)) {
      fail("pattern", `string does not match pattern /${schema.pattern}/`, "/pattern");
    }
  }

  if (typeof value === "number") {
    if (schema.minimum !== undefined && value < schema.minimum) {
      fail("minimum", `number below minimum ${schema.minimum}`, "/minimum");
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      fail("maximum", `number above maximum ${schema.maximum}`, "/maximum");
    }
    if (schema.exclusiveMinimum !== undefined && value <= schema.exclusiveMinimum) {
      fail("exclusiveMinimum", `number not above exclusiveMinimum ${schema.exclusiveMinimum}`, "/exclusiveMinimum");
    }
    if (schema.exclusiveMaximum !== undefined && value >= schema.exclusiveMaximum) {
      fail("exclusiveMaximum", `number not below exclusiveMaximum ${schema.exclusiveMaximum}`, "/exclusiveMaximum");
    }
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      fail("minItems", `array has fewer than minItems ${schema.minItems}`, "/minItems");
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      fail("maxItems", `array has more than maxItems ${schema.maxItems}`, "/maxItems");
    }
    if (schema.items) {
      value.forEach((item, i) => {
        validate(schema.items as Schema, item, `${instancePath}/${i}`, `${schemaPath}/items`, issues);
      });
    }
  }

  if (isPlainObject(value)) {
    for (const key of schema.required ?? []) {
      if (!Object.hasOwn(value, key)) {
        fail("required", `missing required property "${key}"`, "/required");
      }
    }
    const props = schema.properties ?? {};
    for (const [key, propSchema] of Object.entries(props)) {
      if (Object.hasOwn(value, key)) {
        validate(propSchema, value[key], joinPointer(instancePath, key), `${schemaPath}/properties/${escape(key)}`, issues);
      }
    }
    if (schema.additionalProperties === false) {
      for (const key of Object.keys(value)) {
        if (!Object.hasOwn(props, key)) {
          fail("additionalProperties", `unexpected additional property "${key}"`, "/additionalProperties");
        }
      }
    }
  }
}
