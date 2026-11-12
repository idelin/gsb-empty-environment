export {
  parseLockfileText,
  scanLockfile,
} from './src/scanner.ts';
export {
  checkAllResolvedHosts,
  checkResolvedHost,
  DEFAULT_ALLOWED_HOSTS,
  parseLockedPackages,
  realNameFromPath,
  resolveRealName,
} from './src/lockfile.ts';
export {
  evaluateAdvisory,
  evaluateRange,
} from './src/osv.ts';
export {
  compareSemVer,
  compareVersions,
  parseSemVer,
} from './src/semver.ts';
export type {
  LockedPackage,
  LockfileClosureResult,
  LockfilePackageEntry,
  OsvAdvisory,
  OsvAffectedRange,
  OsvSemverEvent,
  PackageEvaluation,
  PackageLock,
  ResolvedHostViolation,
  ScanOptions,
} from './src/types.ts';
