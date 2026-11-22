import type {
  BrowserEnvironment,
  PublicKeyCredentialRequestOptionsLike,
  WindowLike,
} from './types.ts';
import { isSameSite } from './samesite.ts';

export const PUBLICKEY_CREDENTIALS_GET = 'publickey-credentials-get';

/**
 * Error thrown whenever the environment gate blocks a request. The name always
 * mirrors the DOMException name the platform itself would raise, so page code
 * can handle gate failures and real platform failures uniformly.
 */
export class GateError extends Error {
  constructor(
    name: 'NotAllowedError' | 'InvalidStateError' | 'TypeError',
    message: string,
  ) {
    super(message);
    this.name = name;
  }
}

function ancestorOrigins(env: BrowserEnvironment): readonly string[] {
  const list = env.window.location.ancestorOrigins;
  if (list) {
    const origins: string[] = [];
    for (let i = 0; i < list.length; i += 1) {
      origins.push(list[i]);
    }
    return origins;
  }
  return [];
}

/**
 * A frame is trusted when it is itself the top-level frame, or when every
 * frame above it in the ancestor chain is same-site. Cross-site embedders
 * (e.g. a third-party advertising iframe) are rejected before any platform
 * call is made.
 */
export function assertTrustedFrame(env: BrowserEnvironment): void {
  const frame = env.window;
  if (frame.self === frame.top) {
    return;
  }

  const ownOrigin = env.document.location.origin;
  const origins = ancestorOrigins(env);
  if (origins.length > 0) {
    for (const ancestorOrigin of origins) {
      if (!isSameSite(ownOrigin, ancestorOrigin)) {
        throw new GateError(
          'NotAllowedError',
          'Conditional mediation is only available in a trusted top-level or same-site frame',
        );
      }
    }
    return;
  }

  // Fallback for engines without location.ancestorOrigins: walk the frame
  // chain. A cross-origin ancestor throws on property access, which by itself
  // proves the ancestor is not same-origin; same-site still requires an
  // explicit origin comparison where the origin is readable.
  let current: WindowLike | undefined = frame.parent;
  let depth = 0;
  while (current && current !== frame.self && depth < 128) {
    let ancestorOrigin: string;
    try {
      ancestorOrigin = current.location.origin;
    } catch {
      throw new GateError(
        'NotAllowedError',
        'Conditional mediation is only available in a trusted top-level or same-site frame',
      );
    }
    if (!isSameSite(ownOrigin, ancestorOrigin)) {
      throw new GateError(
        'NotAllowedError',
        'Conditional mediation is only available in a trusted top-level or same-site frame',
      );
    }
    if (current === current.parent) {
      break;
    }
    current = current.parent;
    depth += 1;
  }
}

function policyAllows(env: BrowserEnvironment): boolean | undefined {
  const policy = env.document.permissionsPolicy ?? env.document.featurePolicy;
  if (!policy) {
    return undefined;
  }
  return policy.allowsFeature(PUBLICKEY_CREDENTIALS_GET);
}

/**
 * The Permissions Policy must explicitly allow publickey-credentials-get.
 * When the platform exposes no policy object we fall back to the Permissions
 * API; when neither exists, top-level frames are allowed while embedded
 * frames fail closed because their embedding allowance cannot be proven.
 */
export async function assertPolicyAllowsGet(env: BrowserEnvironment): Promise<void> {
  const policyResult = policyAllows(env);
  if (policyResult !== undefined) {
    if (!policyResult) {
      throw new GateError(
        'NotAllowedError',
        'Permissions policy disallows "publickey-credentials-get"',
      );
    }
    return;
  }

  const permissions = env.navigator.permissions;
  if (permissions?.query) {
    const result = await permissions.query({ name: PUBLICKEY_CREDENTIALS_GET });
    if (result.state === 'denied') {
      throw new GateError(
        'NotAllowedError',
        'Permissions policy disallows "publickey-credentials-get"',
      );
    }
    return;
  }

  if (env.window.self !== env.window.top) {
    throw new GateError(
      'NotAllowedError',
      'Cannot prove "publickey-credentials-get" is allowed in this embedded frame',
    );
  }
}

/**
 * Conditional mediation requires an empty allowCredentials list: the browser
 * must be free to surface any discoverable passkey from its autofill UI.
 * A populated list is a caller error, not something to silently truncate.
 */
export function assertEmptyAllowCredentials(
  options: PublicKeyCredentialRequestOptionsLike,
): void {
  if (!options || typeof options !== 'object') {
    throw new GateError('TypeError', 'A "publicKey" options object is required');
  }
  if (options.allowCredentials !== undefined && options.allowCredentials !== null) {
    if (!Array.isArray(options.allowCredentials) || options.allowCredentials.length > 0) {
      throw new GateError(
        'InvalidStateError',
        'Conditional mediation requires an empty "allowCredentials" list',
      );
    }
  }
  if (options.challenge === undefined || options.challenge === null) {
    throw new GateError('TypeError', 'A "challenge" is required');
  }
}
