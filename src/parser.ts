import type {
  AndroidManifest,
  ComponentInfo,
  ComponentType,
  DataSpec,
  IntentFilter,
  PermissionDeclaration,
} from './types.ts';

interface RawElement {
  tag: string;
  attrs: Record<string, string>;
  closing?: boolean;
  selfClosing?: boolean;
}

const COMPONENT_TAGS = new Set<ComponentType>([
  'activity',
  'activity-alias',
  'service',
  'receiver',
  'provider',
]);

function booleanAttr(value: string | undefined): boolean | undefined {
  if (value === undefined) {
    return undefined;
  }
  return value === 'true';
}

function splitList(value: string | undefined): string[] {
  if (!value) {
    return [];
  }
  return value
    .split(/[;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
}

export function tokenizeManifest(xml: string): RawElement[] {
  const stripped = xml
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<\?[\s\S]*?\?>/g, '');

  const elements: RawElement[] = [];
  const tagPattern = /<\/?([a-zA-Z][\w.-]*)((?:\s+[^<>]*?)?)(\/?)>/gs;
  let match: RegExpExecArray | null;
  while ((match = tagPattern.exec(stripped)) !== null) {
    const closing = stripped[match.index + 1] === '/';
    const tag = match[1];
    const attrText = match[2] ?? '';
    const selfClosing = (match[3] ?? '') === '/';

    const attrs: Record<string, string> = {};
    const attrPattern =
      /([\w:.-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/gs;
    let attrMatch: RegExpExecArray | null;
    while ((attrMatch = attrPattern.exec(attrText)) !== null) {
      const rawName = attrMatch[1];
      const localName = rawName.includes(':')
        ? rawName.slice(rawName.lastIndexOf(':') + 1)
        : rawName;
      attrs[localName] = attrMatch[2] ?? attrMatch[3] ?? attrMatch[4] ?? '';
    }

    elements.push({ tag, attrs, closing, selfClosing });
  }
  return elements;
}

function emptyFilter(): IntentFilter {
  return { actions: [], categories: [], data: [] };
}

function emptyComponent(type: ComponentType): ComponentInfo {
  return {
    type,
    name: '',
    explicitExported: false,
    authorities: [],
    intentFilters: [],
    metaData: {},
  };
}

export function parseManifest(xml: string): AndroidManifest {
  const elements = tokenizeManifest(xml);

  let packageName = '';
  let targetSdk: number | undefined;
  let minSdk: number | undefined;
  let applicationPermission: string | undefined;
  const components: ComponentInfo[] = [];
  const permissions = new Map<string, PermissionDeclaration>();
  const usesPermissions: string[] = [];

  const stack: Array<{ tag: string; component?: ComponentInfo; filter?: IntentFilter }> = [];

  const resolveName = (rawName: string | undefined): string => {
    if (!rawName) {
      return '';
    }
    if (rawName.startsWith('.') || !rawName.includes('.')) {
      const suffix = rawName.startsWith('.') ? rawName : `.${rawName}`;
      return `${packageName}${suffix}`;
    }
    return rawName;
  };

  for (const element of elements) {
    const isClosing = element.closing === true;
    const isSelfClosing = element.selfClosing === true;
    const { tag, attrs } = element;

    if (isClosing) {
      stack.pop();
      continue;
    }

    const parent = stack[stack.length - 1];

    if (tag === 'manifest') {
      packageName = attrs.package ?? '';
      stack.push({ tag });
      continue;
    }

    if (tag === 'uses-sdk') {
      targetSdk = attrs.targetSdkVersion ? Number(attrs.targetSdkVersion) : undefined;
      minSdk = attrs.minSdkVersion ? Number(attrs.minSdkVersion) : undefined;
      if (!isSelfClosing) {
        stack.push({ tag });
      }
      continue;
    }

    if (tag === 'uses-permission') {
      const name = attrs.name;
      if (name) {
        usesPermissions.push(name);
      }
      if (!isSelfClosing) {
        stack.push({ tag });
      }
      continue;
    }

    if (tag === 'permission') {
      const name = attrs.name ?? '';
      const rawLevel = attrs.protectionLevel;
      const defaulted = rawLevel === undefined;
      const protectionLevels = rawLevel
        ? rawLevel.split('|').map((level) => level.trim()).filter(Boolean)
        : ['normal'];
      if (name) {
        permissions.set(name, { name, protectionLevels, defaulted });
      }
      if (!isSelfClosing) {
        stack.push({ tag });
      }
      continue;
    }

    if (tag === 'application') {
      applicationPermission = attrs.permission;
      stack.push({ tag });
      continue;
    }

    if (COMPONENT_TAGS.has(tag as ComponentType)) {
      const component = emptyComponent(tag as ComponentType);
      component.name = resolveName(attrs.name);
      component.exported = booleanAttr(attrs.exported);
      component.explicitExported = attrs.exported !== undefined;
      component.permission = attrs.permission;
      if (tag === 'provider') {
        component.readPermission = attrs.readPermission;
        component.writePermission = attrs.writePermission;
        component.grantUriPermissions = booleanAttr(attrs.grantUriPermissions);
        component.authorities = splitList(attrs.authorities);
      }
      if (tag === 'activity-alias') {
        component.targetActivity = resolveName(attrs.targetActivity);
      }
      components.push(component);
      if (!isSelfClosing) {
        stack.push({ tag, component });
      }
      continue;
    }

    if (tag === 'intent-filter') {
      const filter = emptyFilter();
      parent?.component?.intentFilters.push(filter);
      if (!isSelfClosing) {
        stack.push({ tag, component: parent?.component, filter });
      }
      continue;
    }

    if (tag === 'action') {
      const filter = stack.findLast((frame) => frame.filter !== undefined)?.filter;
      if (attrs.name) {
        filter?.actions.push(attrs.name);
      }
      continue;
    }

    if (tag === 'category') {
      const filter = stack.findLast((frame) => frame.filter !== undefined)?.filter;
      if (attrs.name) {
        filter?.categories.push(attrs.name);
      }
      continue;
    }

    if (tag === 'data') {
      const dataSpec: DataSpec = {
        scheme: attrs.scheme,
        host: attrs.host,
        port: attrs.port,
        path: attrs.path,
        pathPrefix: attrs.pathPrefix,
        pathPattern: attrs.pathPattern,
        mimeType: attrs.mimeType,
      };
      const filter = stack.findLast((frame) => frame.filter !== undefined)?.filter;
      filter?.data.push(dataSpec);
      continue;
    }

    if (tag === 'meta-data') {
      const metaName = attrs.name;
      if (metaName && parent?.component) {
        parent.component.metaData[metaName] = attrs.resource ?? attrs.value ?? '';
      }
      continue;
    }

    if (!isSelfClosing) {
      stack.push({ tag });
    }
  }

  return {
    packageName,
    targetSdk,
    minSdk,
    applicationPermission,
    components,
    permissions,
    usesPermissions,
  };
}
