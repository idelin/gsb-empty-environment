import type {
  ApprovalOptions,
  NavigationState,
  SubmitPlan,
  TargetPolicy,
  Transport,
  TransportResult,
} from "./types.ts";
import { SubmitError } from "./types.ts";
import { parseHttpOrigin } from "./url.ts";
import { prepareRequest } from "./request.ts";

/**
 * Verify a plan against the *live* document immediately before dispatch.
 *
 * - Document generation must match: any navigation invalidates the plan.
 * - The live document origin must still equal the approved origin.
 * - The target policy is evaluated again, so a permission that was revoked
 *   between approval and submission cannot be used.
 */
export function verifyPlan(
  plan: SubmitPlan,
  state: NavigationState,
  options: ApprovalOptions = {},
): void {
  if (state.generation !== plan.documentGeneration) {
    throw new SubmitError(
      "STALE_GENERATION",
      `document generation changed: approved ${plan.documentGeneration}, live ${state.generation}`,
    );
  }

  const liveOrigin = parseHttpOrigin(state.origin);
  if (liveOrigin.origin !== plan.documentOrigin) {
    throw new SubmitError(
      "ORIGIN_MISMATCH",
      `document origin changed: approved ${plan.documentOrigin}, live ${liveOrigin.origin}`,
    );
  }

  if (options.policy) {
    const policy: TargetPolicy = options.policy;
    const allowed = policy.allow({
      documentOrigin: plan.documentOrigin,
      targetOrigin: plan.target.origin,
      absoluteUrl: plan.target.absoluteUrl,
      method: plan.target.method,
      enctype: plan.target.enctype,
      submitter: plan.submitter,
    });
    if (!allowed) {
      throw new SubmitError(
        "PERMISSION_REVOKED",
        `target policy no longer allows ${plan.target.absoluteUrl}`,
      );
    }
  }
}

function sameRequestUrl(approved: string, actual: string): boolean {
  try {
    return new URL(actual).href === new URL(approved).href;
  } catch {
    return false;
  }
}

export interface SubmitOutcome {
  result: TransportResult;
  requestUrl: string;
}

/**
 * Submit an approved plan. All mutable document state is read through
 * {@link readState}; a navigation racing the dispatch is detected either by
 * the generation check before the call or by the transport's reported URL
 * afterwards.
 */
export async function submitPlan(
  plan: SubmitPlan,
  readState: () => NavigationState,
  transport: Transport,
  options: ApprovalOptions = {},
): Promise<SubmitOutcome> {
  const state = readState();
  verifyPlan(plan, state, options);

  const request = await prepareRequest(plan);
  const result = await transport.dispatch(request);

  if (!sameRequestUrl(request.url, result.url)) {
    throw new SubmitError(
      "TARGET_DRIFT",
      `transport contacted ${result.url} but approved target was ${request.url}`,
    );
  }

  return { result, requestUrl: request.url };
}
