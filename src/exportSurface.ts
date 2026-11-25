import type {
  AndroidManifest,
  ComponentInfo,
  ComponentSurface,
  Finding,
  ProtectionInfo,
  ProtectionLevel,
} from './types.ts';

const KNOWN_PLATFORM_PERMISSIONS = new Map<string, ProtectionLevel>([
  ['android.permission.READ_CALENDAR', 'dangerous'],
  ['android.permission.WRITE_CALENDAR', 'dangerous'],
  ['android.permission.CAMERA', 'dangerous'],
  ['android.permission.READ_CONTACTS', 'dangerous'],
  ['android.permission.WRITE_CONTACTS', 'dangerous'],
  ['android.permission.GET_ACCOUNTS', 'dangerous'],
  ['android.permission.ACCESS_FINE_LOCATION', 'dangerous'],
  ['android.permission.ACCESS_COARSE_LOCATION', 'dangerous'],
  ['android.permission.RECORD_AUDIO', 'dangerous'],
  ['android.permission.READ_PHONE_STATE', 'dangerous'],
  ['android.permission.CALL_PHONE', 'dangerous'],
  ['android.permission.READ_CALL_LOG', 'dangerous'],
  ['android.permission.WRITE_CALL_LOG', 'dangerous'],
  ['android.permission.ADD_VOICEMAIL', 'dangerous'],
  ['android.permission.USE_SIP', 'dangerous'],
  ['android.permission.BODY_SENSORS', 'dangerous'],
  ['android.permission.SEND_SMS', 'dangerous'],
  ['android.permission.RECEIVE_SMS', 'dangerous'],
  ['android.permission.READ_SMS', 'dangerous'],
  ['android.permission.RECEIVE_WAP_PUSH', 'dangerous'],
  ['android.permission.RECEIVE_MMS', 'dangerous'],
  ['android.permission.READ_EXTERNAL_STORAGE', 'dangerous'],
  ['android.permission.WRITE_EXTERNAL_STORAGE', 'dangerous'],
  ['android.permission.READ_MEDIA_IMAGES', 'dangerous'],
  ['android.permission.READ_MEDIA_VIDEO', 'dangerous'],
  ['android.permission.READ_MEDIA_AUDIO', 'dangerous'],
  ['android.permission.POST_NOTIFICATIONS', 'dangerous'],
  ['android.permission.NEARBY_WIFI_DEVICES', 'dangerous'],
  ['android.permission.BLUETOOTH_SCAN', 'dangerous'],
  ['android.permission.BLUETOOTH_CONNECT', 'dangerous'],
  ['android.permission.INTERNET', 'normal'],
  ['android.permission.ACCESS_NETWORK_STATE', 'normal'],
  ['android.permission.VIBRATE', 'normal'],
  ['android.permission.WAKE_LOCK', 'normal'],
  ['android.permission.RECEIVE_BOOT_COMPLETED', 'normal'],
  ['android.permission.FOREGROUND_SERVICE', 'normal'],
  ['android.permission.FOREGROUND_SERVICE_DATA_SYNC', 'normal'],
  ['android.permission.BIND_DEVICE_ADMIN', 'signature'],
  ['android.permission.BIND_NOTIFICATION_LISTENER_SERVICE', 'signature'],
  ['android.permission.BIND_ACCESSIBILITY_SERVICE', 'signature'],
  ['android.permission.BIND_INPUT_METHOD', 'signature'],
  ['android.permission.BIND_VPN_SERVICE', 'signature'],
  ['android.permission.BIND_WALLPAPER', 'signature'],
  ['android.permission.BIND_TELECOM_CONNECTION_SERVICE', 'signature'],
  ['android.permission.BIND_NFC_SERVICE', 'signature'],
  ['android.permission.BIND_PRINT_SERVICE', 'signature'],
  ['android.permission.READ_DEVICE_CONFIG', 'signature'],
  ['android.permission.INTERACT_ACROSS_USERS_FULL', 'signature'],
  ['android.permission.MANAGE_USERS', 'signature'],
  ['android.permission.CREATE_USERS', 'signature'],
  ['android.permission.START_ACTIVITIES_FROM_BACKGROUND', 'signature'],
  ['android.permission.PROVIDE_TRUST_AGENT', 'signature'],
  ['android.permission.PROVIDE_AUTOFILL_SERVICE', 'signature'],
  ['android.permission.PROVIDE_INLINE_SUGGESTION_SERVICE', 'signature'],
]);

const SIGNATURE_LEVEL_TOKENS = new Set([
  'signature',
  'signatureOrSystem',
  'privileged',
  'internal',
]);

export function isSignatureLevel(level: ProtectionLevel | undefined): boolean {
  return (
    level === 'signature' ||
    level === 'signatureOrSystem' ||
    level === 'privileged' ||
    level === 'internal'
  );
}

function isDangerouslyGrantable(level: ProtectionLevel): boolean {
  return level === 'normal' || level === 'dangerous' || level === 'unknown';
}

export interface EffectiveExport {
  exported: boolean;
  implicit: boolean;
  reason: string;
}

export function resolveEffectiveExport(
  component: ComponentInfo,
  targetSdk: number | undefined,
): EffectiveExport {
  const hasFilter = component.intentFilters.length > 0;

  if (component.explicitExported) {
    if (component.exported === true) {
      return {
        exported: true,
        implicit: false,
        reason: 'explicit android:exported="true"',
      };
    }
    return {
      exported: false,
      implicit: false,
      reason: 'explicit android:exported="false"',
    };
  }

  if (component.type === 'activity-alias') {
    if (hasFilter) {
      return {
        exported: true,
        implicit: true,
        reason: 'no android:exported; activity-alias with intent-filter is implicitly exported',
      };
    }
    return {
      exported: targetSdk === undefined || targetSdk < 31,
      implicit: true,
      reason:
        targetSdk !== undefined && targetSdk >= 31
          ? 'activity-alias without android:exported is invalid on targetSdk >= 31'
          : 'activity-alias defaults to exported when android:exported is absent',
    };
  }

  if (hasFilter) {
    const implicitExported = targetSdk === undefined || targetSdk < 31;
    return {
      exported: implicitExported,
      implicit: true,
      reason:
        implicitExported
          ? 'no android:exported; components with an intent-filter are implicitly exported below targetSdk 31'
          : 'no android:exported with an intent-filter; installation/export rejected on targetSdk >= 31',
    };
  }

  return {
    exported: false,
    implicit: false,
    reason: 'no intent-filter and no android:exported; component defaults to private',
  };
}

export interface PermissionResolver {
  resolve(permission: string | undefined): ProtectionInfo;
}

export function createPermissionResolver(
  manifest: AndroidManifest,
  registryOverrides: Record<string, ProtectionLevel> = {},
): PermissionResolver {
  const registry = new Map<string, ProtectionLevel>([
    ...KNOWN_PLATFORM_PERMISSIONS,
    ...Object.entries(registryOverrides),
  ]);

  const resolveLevel = (permission: string): { level: ProtectionLevel; customDeclared: boolean; defaultedLevel: boolean } => {
    const declared = manifest.permissions.get(permission);
    if (declared) {
      const strongest = declared.protectionLevels.some((token) =>
        SIGNATURE_LEVEL_TOKENS.has(token),
      )
        ? 'signature'
        : declared.protectionLevels.includes('dangerous')
          ? 'dangerous'
          : 'normal';
      return {
        level: strongest,
        customDeclared: true,
        defaultedLevel: declared.defaulted,
      };
    }

    const platformLevel = registry.get(permission);
    if (platformLevel) {
      return {
        level: platformLevel,
        customDeclared: false,
        defaultedLevel: false,
      };
    }

    return {
      level: 'unknown',
      customDeclared: false,
      defaultedLevel: false,
    };
  };

  return {
    resolve(permission: string | undefined): ProtectionInfo {
      const effectivePermission = permission ?? manifest.applicationPermission;
      if (!effectivePermission) {
        return {
          level: 'normal',
          customDeclared: false,
          defaultedLevel: false,
        };
      }
      const resolved = resolveLevel(effectivePermission);
      return {
        permission: effectivePermission,
        level: resolved.level,
        customDeclared: resolved.customDeclared,
        defaultedLevel: resolved.defaultedLevel,
      };
    },
  };
}

export interface SurfaceResult {
  surfaces: ComponentSurface[];
  findings: Finding[];
}

export function analyzeExportSurface(
  manifest: AndroidManifest,
  registryOverrides: Record<string, ProtectionLevel> = {},
): SurfaceResult {
  const resolver = createPermissionResolver(manifest, registryOverrides);
  const surfaces: ComponentSurface[] = [];
  const findings: Finding[] = [];

  for (const component of manifest.components) {
    const effective = resolveEffectiveExport(component, manifest.targetSdk);
    const protection = resolver.resolve(component.permission);
    const surface: ComponentSurface = {
      name: component.name,
      type: component.type,
      exported: effective.exported,
      explicitExported: component.explicitExported,
      implicitlyExported: effective.implicit && effective.exported,
      reason: effective.reason,
      protection: {
        ...protection,
        readPermission: component.readPermission,
        writePermission: component.writePermission,
      },
      reachableViaRedirect: false,
      redirectEntries: [],
    };
    surfaces.push(surface);

    if (
      !component.explicitExported &&
      manifest.targetSdk !== undefined &&
      manifest.targetSdk >= 31 &&
      (component.intentFilters.length > 0 ||
        component.type === 'activity-alias')
    ) {
      findings.push({
        id: `missing-exported-attr:${component.name}`,
        severity: 'high',
        type: 'missing-exported-attribute',
        component: component.name,
        message: `${component.type} declares an intent-filter without android:exported; the package is rejected on targetSdk >= 31`,
        evidence: {
          targetSdk: manifest.targetSdk,
          intentFilterCount: component.intentFilters.length,
        },
      });
    }

    if (!effective.exported) {
      continue;
    }

    if (component.type === 'provider') {
      const readGuard = component.readPermission ?? component.permission;
      const writeGuard = component.writePermission ?? component.permission;
      if (!readGuard || !writeGuard) {
        findings.push({
          id: `unprotected-provider:${component.name}`,
          severity: 'high',
          type: 'unprotected-exported-component',
          component: component.name,
          message: `exported provider lacks a read/write permission guard (read=${readGuard ?? 'none'}, write=${writeGuard ?? 'none'})`,
          evidence: {
            readPermission: readGuard ?? null,
            writePermission: writeGuard ?? null,
            authorities: component.authorities,
            implicit: effective.implicit,
          },
        });
      } else {
        const readInfo = resolver.resolve(readGuard);
        const writeInfo = resolver.resolve(writeGuard);
        if (isDangerouslyGrantable(readInfo.level) || isDangerouslyGrantable(writeInfo.level)) {
          findings.push({
            id: `weak-provider-permission:${component.name}`,
            severity: 'medium',
            type: 'weak-permission-guard',
            component: component.name,
            message: `exported provider is guarded by a third-party-requestable permission (${readInfo.level})`,
            evidence: {
              read: { permission: readGuard, level: readInfo.level },
              write: { permission: writeGuard, level: writeInfo.level },
            },
          });
        }
      }

      if (component.grantUriPermissions === true) {
        findings.push({
          id: `uri-grant-bypass:${component.name}`,
          severity: 'medium',
          type: 'uri-permission-grant-bypass',
          component: component.name,
          message:
            'android:grantUriPermissions="true" lets an exported entry hand over URI access to callers that lack the provider permission',
          evidence: {
            authorities: component.authorities,
            readPermission: readGuard ?? null,
            writePermission: writeGuard ?? null,
          },
        });
      }
      continue;
    }

    if (!protection.permission) {
      findings.push({
        id: `unprotected-export:${component.name}`,
        severity: effective.implicit ? 'critical' : 'high',
        type: 'unprotected-exported-component',
        component: component.name,
        message: effective.implicit
          ? `${component.type} is implicitly exported and has no permission guard`
          : `exported ${component.type} has no permission guard`,
        evidence: {
          implicit: effective.implicit,
          reason: effective.reason,
          intentFilterActions: component.intentFilters.flatMap((filter) => filter.actions),
        },
      });
      continue;
    }

    if (isDangerouslyGrantable(protection.level)) {
      findings.push({
        id: `weak-export-permission:${component.name}`,
        severity: 'medium',
        type: 'weak-permission-guard',
        component: component.name,
        message: `exported ${component.type} relies on ${protection.level} permission '${protection.permission}' that any third-party app can obtain`,
        evidence: {
          permission: protection.permission,
          level: protection.level,
          customDeclared: protection.customDeclared,
          defaultedLevel: protection.defaultedLevel,
        },
      });
    }

    if (protection.defaultedLevel) {
      findings.push({
        id: `defaulted-permission-level:${protection.permission}`,
        severity: 'medium',
        type: 'permission-level-defaulted',
        component: component.name,
        message: `custom permission '${protection.permission}' declares no protectionLevel and defaults to 'normal'`,
        evidence: {
          permission: protection.permission,
        },
      });
    }

    if (!protection.customDeclared && protection.level === 'unknown') {
      findings.push({
        id: `undeclared-permission:${component.name}`,
        severity: 'low',
        type: 'undeclared-permission-reference',
        component: component.name,
        message: `permission '${protection.permission}' is neither declared by this package nor recognized as a platform permission`,
        evidence: {
          permission: protection.permission,
        },
      });
    }
  }

  return { surfaces, findings };
}
