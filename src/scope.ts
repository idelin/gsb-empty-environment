import {
  type NormalizedUrl,
  type ScopeMatch,
  isPathPrefix,
  normalizeUrl,
  sameOrigin,
  scopeFromUrl,
  scriptDirectory,
} from './url.ts';
import { fail } from './errors.ts';

export interface PlanRegistrationInput {
  clientUrl: string;
  scriptUrl: string;
  redirectChain?: readonly string[];
  serviceWorkerAllowed?: string | null;
  requestedScope?: string;
}

export interface RegistrationPlan {
  readonly origin: string;
  readonly scriptUrl: string;
  readonly effectiveScriptUrl: string;
  readonly redirectHops: readonly string[];
  readonly defaultScope: string;
  readonly maxScope: string;
  readonly scope: string;
  readonly defaultScopeSegments: readonly string[];
  readonly maxScopeSegments: readonly string[];
  readonly scopeSegments: readonly string[];
}

function assertSameOrigin(
  reference: NormalizedUrl,
  candidate: NormalizedUrl,
  code: 'CROSS_ORIGIN_SCRIPT' | 'CROSS_ORIGIN_REDIRECT' | 'CROSS_ORIGIN_SCOPE',
  label: string,
): void {
  if (!sameOrigin(reference, candidate)) {
    fail(code, `${label} 与注册来源不同源，禁止跨来源扩张。`, {
      referenceOrigin: reference.origin,
      candidateOrigin: candidate.origin,
    });
  }
}

export function planRegistration(input: PlanRegistrationInput): RegistrationPlan {
  const client = normalizeUrl(input.clientUrl);
  const initialScript = normalizeUrl(input.scriptUrl, input.clientUrl);
  assertSameOrigin(client, initialScript, 'CROSS_ORIGIN_SCRIPT', '脚本 URL');

  const redirectHops: NormalizedUrl[] = [];
  for (const hop of input.redirectChain ?? []) {
    const hopUrl = normalizeUrl(hop, input.clientUrl);
    assertSameOrigin(client, hopUrl, 'CROSS_ORIGIN_REDIRECT', '重定向跳转 URL');
    redirectHops.push(hopUrl);
  }

  const effectiveScript =
    redirectHops.length > 0 ? redirectHops[redirectHops.length - 1] : initialScript;

  const defaultScope = scriptDirectory(effectiveScript);

  let maxScope: ScopeMatch = defaultScope;
  if (input.serviceWorkerAllowed !== undefined && input.serviceWorkerAllowed !== null) {
    const headerUrl = normalizeUrl(input.serviceWorkerAllowed, input.clientUrl);
    assertSameOrigin(
      client,
      headerUrl,
      'CROSS_ORIGIN_SCOPE',
      'Service-Worker-Allowed 作用域',
    );
    const headerScope = scopeFromUrl(headerUrl);
    if (!isPathPrefix(headerScope.scopeSegments, defaultScope.scopeSegments)) {
      fail(
        'HEADER_SCOPE_NOT_ANCESTOR',
        'Service-Worker-Allowed 只能声明脚本目录的祖先目录，不能借头扩张到无关分支。',
        {
          headerScope: headerScope.scopePath,
          scriptDirectory: defaultScope.scopePath,
        },
      );
    }
    maxScope = headerScope;
  }

  let requested: ScopeMatch;
  if (input.requestedScope === undefined) {
    requested = defaultScope;
  } else {
    const requestedUrl = normalizeUrl(input.requestedScope, input.clientUrl);
    assertSameOrigin(client, requestedUrl, 'CROSS_ORIGIN_SCOPE', '请求作用域');
    requested = scopeFromUrl(requestedUrl);

    const withinDefault = isPathPrefix(defaultScope.scopeSegments, requested.scopeSegments);
    if (!withinDefault) {
      if (input.serviceWorkerAllowed === undefined || input.serviceWorkerAllowed === null) {
        fail(
          'SCOPE_OUTSIDE_DEFAULT',
          '没有 Service-Worker-Allowed 头时，作用域不能超出脚本所在目录。',
          {
            requestedScope: requested.scopePath,
            defaultScope: defaultScope.scopePath,
          },
        );
      }
      if (!isPathPrefix(maxScope.scopeSegments, requested.scopeSegments)) {
        fail(
          'SCOPE_OUTSIDE_ALLOWED_HEADER',
          '请求作用域超出 Service-Worker-Allowed 允许的最大作用域。',
          {
            requestedScope: requested.scopePath,
            maxScope: maxScope.scopePath,
          },
        );
      }
    }
  }

  if (!isPathPrefix(maxScope.scopeSegments, requested.scopeSegments)) {
    fail(
      'SCOPE_OUTSIDE_ALLOWED_HEADER',
      '请求作用域超出允许的最大作用域。',
      {
        requestedScope: requested.scopePath,
        maxScope: maxScope.scopePath,
      },
    );
  }

  return {
    origin: client.origin,
    scriptUrl: initialScript.pathname,
    effectiveScriptUrl: effectiveScript.pathname,
    redirectHops: [initialScript, ...redirectHops].map((hop) => hop.pathname),
    defaultScope: defaultScope.scopePath,
    maxScope: maxScope.scopePath,
    scope: requested.scopePath,
    defaultScopeSegments: defaultScope.scopeSegments,
    maxScopeSegments: maxScope.scopeSegments,
    scopeSegments: requested.scopeSegments,
  };
}
