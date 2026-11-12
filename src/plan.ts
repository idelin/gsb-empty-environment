import type {
  ApprovalOptions,
  ButtonOverride,
  FormEntry,
  FormSnapshot,
  PolicyContext,
  SubmitPlan,
  TargetPolicy,
} from "./types.ts";
import { SubmitError } from "./types.ts";
import { isSameOrigin, parseHttpOrigin } from "./url.ts";
import { deepFreeze, resolveSubmission } from "./snapshot.ts";

const DEFAULT_METHODS = ["GET", "POST"] as const;
const DEFAULT_ENCTYPES = [
  "application/x-www-form-urlencoded",
  "multipart/form-data",
  "text/plain",
] as const;

let planCounter = 0;

function nextPlanId(): string {
  planCounter += 1;
  const time = Date.now().toString(36);
  const seq = planCounter.toString(36).padStart(4, "0");
  return `plan_${time}_${seq}_${crypto.randomUUID()}`;
}

class SameOriginPolicy implements TargetPolicy {
  allow(context: PolicyContext): boolean {
    const origin = parseHttpOrigin(context.documentOrigin);
    const target = parseHttpOrigin(context.targetOrigin);
    return isSameOrigin(origin, target);
  }
}

function checkAllowed(method: string, enctype: string, options: ApprovalOptions): void {
  const methods: readonly string[] = options.allowedMethods ?? DEFAULT_METHODS;
  if (!methods.includes(method)) {
    throw new SubmitError(
      "METHOD_NOT_ALLOWED",
      `method ${method} is not in the allow list: ${methods.join(", ")}`,
    );
  }
  const encTypes: readonly string[] = options.allowedEncTypes ?? DEFAULT_ENCTYPES;
  if (!encTypes.includes(enctype)) {
    throw new SubmitError(
      "ENCTYPE_NOT_ALLOWED",
      `enctype ${enctype} is not in the allow list: ${encTypes.join(", ")}`,
    );
  }
}

/**
 * The single approval entry point. Both plain form submissions and button
 * submissions (including formaction/formmethod/formenctype overrides) must go
 * through here; the returned plan has the absolute target, method and encoding
 * frozen before any later document mutation can occur.
 */
export function createSubmitPlan(
  snapshot: FormSnapshot,
  submitter: ButtonOverride | null = null,
  options: ApprovalOptions = {},
): SubmitPlan {
  const policy: TargetPolicy = options.policy ?? new SameOriginPolicy();
  const { actionUrl, method, enctype, submitter: frozenSubmitter } =
    resolveSubmission(snapshot, submitter);

  checkAllowed(method, enctype, options);

  const documentOriginUrl = parseHttpOrigin(snapshot.origin);
  if (documentOriginUrl.protocol !== actionUrl.protocol) {
    throw new SubmitError(
      "UNSAFE_ACTION_URL",
      `scheme mismatch between document and action: ${actionUrl.href}`,
    );
  }

  const entries: FormEntry[] = snapshot.entries.map((entry) => ({
    name: String(entry.name),
    value: entry.value,
    ...(entry.filename === undefined ? {} : { filename: String(entry.filename) }),
  }));
  if (frozenSubmitter && frozenSubmitter.name !== undefined && frozenSubmitter.name !== "") {
    entries.push({
      name: frozenSubmitter.name,
      value: frozenSubmitter.value ?? "",
    });
  }

  const context: PolicyContext = {
    documentOrigin: documentOriginUrl.origin,
    targetOrigin: actionUrl.origin,
    absoluteUrl: actionUrl.href,
    method,
    enctype,
    submitter: frozenSubmitter,
  };

  if (!policy.allow(context)) {
    throw new SubmitError(
      "TARGET_DENIED",
      `target policy denied ${method} ${actionUrl.href}`,
      context,
    );
  }

  const plan: SubmitPlan = {
    id: nextPlanId(),
    documentGeneration: snapshot.documentGeneration,
    documentOrigin: documentOriginUrl.origin,
    baseUrlAtApproval:
      snapshot.baseUrl === "" ? documentOriginUrl.href : new URL(snapshot.baseUrl).href,
    target: {
      absoluteUrl: actionUrl.href,
      origin: actionUrl.origin,
      method,
      enctype,
    },
    submitter: frozenSubmitter,
    entries,
    createdAt: Date.now(),
  };

  return deepFreeze(plan);
}
