export {
  CredentialVault,
  CredentialAccessDenied,
  DEFAULT_POLICY,
  RECOMMENDED_PERMISSIONS_POLICY,
  inputSink,
} from './src/vault.ts';
export type { VaultPolicy, SecretSink, DenyReason } from './src/vault.ts';
export { AuditLog } from './src/audit.ts';
export type { AuditAction, AuditEntry } from './src/audit.ts';
export { readBrowserContext } from './src/context.ts';
export type { CallerContext } from './src/context.ts';
