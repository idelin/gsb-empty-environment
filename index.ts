export { StorageAccessHost } from "./src/host.ts";
export { TransientActivationTracker } from "./src/activation.ts";
export { GrantStore } from "./src/grants.ts";
export {
  ALLOW_STORAGE_ACCESS_BY_USER_ACTIVATION,
  assertSandboxToken,
} from "./src/sandbox.ts";
export { StorageAccessError } from "./src/errors.ts";
export { sameSite, siteOf, grantKey } from "./src/site.ts";
export type {
  Clock,
  FrameContext,
  FrameId,
  GrantKey,
  SandboxTokenSet,
  Site,
  StorageAccessHostOptions,
} from "./src/types.ts";
