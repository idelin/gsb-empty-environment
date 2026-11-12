import { ConditionalMediationGate } from './src/gate.ts';
import { createBrowserEnvironment } from './src/browserEnv.ts';
import type {
  GateEnvironment,
  PublicKeyCredentialRequestOptionsLike,
} from './src/types.ts';

export { ConditionalMediationGate } from './src/gate.ts';
export { createBrowserEnvironment } from './src/browserEnv.ts';
export { isSameSite, registrableDomain } from './src/sameSite.ts';
export {
  CONDITIONAL_MEDIATION,
  PUBLICKEY_CREDENTIALS_GET_PERMISSION,
  GateError,
} from './src/types.ts';
export type {
  GateEnvironment,
  GateErrorCode,
  PublicKeyCredentialRequestOptionsLike,
  ConditionalCredentialRequest,
} from './src/types.ts';

let defaultGate: ConditionalMediationGate | null = null;

/**
 * Page-script entry point: runs the full environment gate and starts a
 * conditional mediation request only when every check passes.
 */
export function conditionalGet(
  publicKey: PublicKeyCredentialRequestOptionsLike,
  env?: GateEnvironment,
): Promise<unknown> {
  const gate =
    env !== undefined
      ? new ConditionalMediationGate(env)
      : (defaultGate ??= new ConditionalMediationGate(
          createBrowserEnvironment(),
        ));
  return gate.conditionalGet(publicKey);
}
