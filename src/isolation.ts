import {
  parseStructuredFieldItem,
  type StructuredFieldBareItem,
} from "./structured-fields.ts";

export interface HeaderPair {
  readonly name: string;
  readonly value: string;
}

type HeaderEntry = HeaderPair | readonly [string, string];

export type HeaderSource =
  | Iterable<HeaderEntry>
  | {
      forEach(callback: (value: string, key: string) => void): void;
    }
  | Record<string, string | readonly string[] | null | undefined>;

export type IsolationHeader =
  | "Cross-Origin-Opener-Policy"
  | "Cross-Origin-Embedder-Policy";

export type PolicyStatus =
  | "effective"
  | "ineffective"
  | "missing"
  | "duplicate"
  | "invalid";

export interface PolicyEvaluation {
  readonly header: IsolationHeader;
  readonly values: readonly string[];
  readonly value: string | null;
  readonly status: PolicyStatus;
  readonly effective: boolean;
}

export interface IsolationFailure {
  readonly header: IsolationHeader;
  readonly reason: Exclude<PolicyStatus, "effective">;
}

export interface CrossOriginIsolationResult {
  readonly isolated: boolean;
  readonly sharedArrayBuffer: boolean;
  readonly coop: PolicyEvaluation;
  readonly coep: PolicyEvaluation;
  readonly failures: readonly IsolationFailure[];
}

interface CollectedHeaders {
  readonly values: Map<string, string[]>;
  readonly malformed: Set<string>;
  readonly joined: Set<string>;
}

const COOP_HEADER = "Cross-Origin-Opener-Policy";
const COEP_HEADER = "Cross-Origin-Embedder-Policy";
const COOP_KEY = COOP_HEADER.toLowerCase();
const COEP_KEY = COEP_HEADER.toLowerCase();

const isHeaderEntry = (value: unknown): value is HeaderEntry => {
  if (Array.isArray(value)) {
    return value.length >= 2 &&
      typeof value[0] === "string" &&
      typeof value[1] === "string";
  }

  return typeof value === "object" && value !== null &&
    "name" in value && "value" in value &&
    typeof (value as HeaderPair).name === "string" &&
    typeof (value as HeaderPair).value === "string";
};

const addHeaderValue = (
  collected: CollectedHeaders,
  name: string,
  value: unknown,
): void => {
  if (typeof name !== "string") {
    return;
  }

  const key = name.toLowerCase();
  if (typeof value !== "string") {
    collected.malformed.add(key);
    return;
  }

  const values = collected.values.get(key);
  if (values === undefined) {
    collected.values.set(key, [value]);
  } else {
    values.push(value);
  }
};

const collectHeaders = (source: HeaderSource | null | undefined): CollectedHeaders => {
  const collected: CollectedHeaders = {
    values: new Map<string, string[]>(),
    malformed: new Set<string>(),
    joined: new Set<string>(),
  };

  if (source === null || source === undefined) {
    return collected;
  }

  const iterable = source as Partial<Iterable<unknown>>;
  if (typeof iterable[Symbol.iterator] === "function") {
    const isHeaders = Object.prototype.toString.call(source) === "[object Headers]";
    for (const entry of iterable as Iterable<unknown>) {
      if (!isHeaderEntry(entry)) {
        continue;
      }

      const name = "name" in entry ? entry.name : entry[0];
      const value = "name" in entry ? entry.value : entry[1];
      addHeaderValue(collected, name, value);
      if (isHeaders) {
        collected.joined.add(name.toLowerCase());
      }
    }
    return collected;
  }

  const withForEach = source as {
    forEach?: (callback: (value: string, key: string) => void) => void;
  };
  if (typeof withForEach.forEach === "function") {
    withForEach.forEach((value, key) => {
      addHeaderValue(collected, key, value);
      collected.joined.add(key.toLowerCase());
    });
    return collected;
  }

  for (const [name, value] of Object.entries(source)) {
    if (Array.isArray(value)) {
      for (const item of value) {
        addHeaderValue(collected, name, item);
      }
    } else if (value !== null && value !== undefined) {
      addHeaderValue(collected, name, value);
    }
  }

  return collected;
};

const getPolicyValue = (
  item: StructuredFieldBareItem,
): string | null => item.type === "token" ? item.value : null;

const evaluatePolicy = (
  collected: CollectedHeaders,
  header: IsolationHeader,
  key: string,
  expectedToken: string,
): PolicyEvaluation => {
  const values = collected.values.get(key) ?? [];
  const malformed = collected.malformed.has(key);

  if (values.length === 0 && !malformed) {
    return {
      header,
      values,
      value: null,
      status: "missing",
      effective: false,
    };
  }

  if (values.length !== 1 || malformed) {
    return {
      header,
      values,
      value: null,
      status: values.length > 1 ? "duplicate" : "invalid",
      effective: false,
    };
  }

  const rawValue = values[0];
  if (rawValue === undefined) {
    return {
      header,
      values,
      value: null,
      status: "invalid",
      effective: false,
    };
  }

  const parsed = parseStructuredFieldItem(rawValue);
  if (parsed === null) {
    return {
      header,
      values,
      value: null,
      status: "invalid",
      effective: false,
    };
  }

  if (collected.joined.has(key) && rawValue.includes(",")) {
    return {
      header,
      values,
      value: null,
      status: "invalid",
      effective: false,
    };
  }

  const reportTo = parsed.parameters.get("report-to");
  if (reportTo !== undefined && reportTo.type !== "string") {
    return {
      header,
      values,
      value: null,
      status: "invalid",
      effective: false,
    };
  }

  const value = getPolicyValue(parsed.item);
  if (value === null) {
    return {
      header,
      values,
      value: null,
      status: "invalid",
      effective: false,
    };
  }

  const effective = value === expectedToken;
  return {
    header,
    values,
    value,
    status: effective ? "effective" : "ineffective",
    effective,
  };
};

const failureFor = (policy: PolicyEvaluation): IsolationFailure | null =>
  policy.effective
    ? null
    : {
        header: policy.header,
        reason: policy.status as Exclude<PolicyStatus, "effective">,
      };

export const evaluateCrossOriginIsolation = (
  headers: HeaderSource | null | undefined,
): CrossOriginIsolationResult => {
  const collected = collectHeaders(headers);
  const coop = evaluatePolicy(
    collected,
    COOP_HEADER,
    COOP_KEY,
    "same-origin",
  );
  const coep = evaluatePolicy(
    collected,
    COEP_HEADER,
    COEP_KEY,
    "require-corp",
  );
  const failures = [failureFor(coop), failureFor(coep)].filter(
    (failure): failure is IsolationFailure => failure !== null,
  );
  const isolated = coop.effective && coep.effective;

  return {
    isolated,
    sharedArrayBuffer: isolated,
    coop,
    coep,
    failures,
  };
};

export const isCrossOriginIsolated = (
  headers: HeaderSource | null | undefined,
): boolean => evaluateCrossOriginIsolation(headers).isolated;

export const canUseSharedArrayBuffer = (
  headers: HeaderSource | null | undefined,
): boolean => evaluateCrossOriginIsolation(headers).sharedArrayBuffer;

export const checkCrossOriginIsolation = evaluateCrossOriginIsolation;
