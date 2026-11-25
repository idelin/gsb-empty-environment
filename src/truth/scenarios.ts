import { doc, leaf, nil, obj, ref, tomb } from '../dsl.ts';
import type { ErrorCode, Facet, Grant, MergeInput } from '../types.ts';

export interface ExpectedError {
  code: ErrorCode;
  pointer?: string;
  facet?: Facet;
}

export interface LeafCheck {
  pointer: string;
  value?: unknown;
  branchId?: string;
  viaIncludes?: string[];
  authorized?: boolean;
  exposed?: boolean;
}

export interface CanonicalExpectation {
  ok: boolean;
  tree?: Record<string, unknown>;
  errorIncludes?: ExpectedError[];
  leafChecks?: LeafCheck[];
  publicationIdPrefix?: string;
}

export interface CanonicalScenario {
  id: string;
  title: string;
  input: MergeInput;
  expect: CanonicalExpectation;
}

function all(issuer: string, path: string, extra: Partial<Grant> = {}): Grant {
  return { issuer, path, facets: '*', ...extra };
}

/* ------------------------------------------------------------------ */
/* 1. Signed subtree moved to another tenant / location               */
/* ------------------------------------------------------------------ */

function origFeatDoc() {
  return doc(
    'orig',
    obj({
      features: obj({
        feat: obj(
          { toggle: leaf(true, { id: 'feat-toggle' }), limit: leaf(3, { id: 'feat-limit' }) },
          { id: 'feat-root' },
        ),
      }),
    }),
    { rootId: 'o-root', attribution: { issuer: 'acme', sigId: 's-acme' } },
  );
}

function movedReject(): CanonicalScenario {
  const moved = doc(
    'moved',
    obj({
      tenants: obj({
        globex: obj({
          features: obj({
            feat: obj(
              { toggle: leaf(true, { id: 'feat-toggle' }), limit: leaf(3, { id: 'feat-limit' }) },
              { id: 'feat-root', issuer: 'acme', sig: 's-acme' },
            ),
          }),
        }),
      }),
    }),
    { rootId: 'm-root', attribution: { issuer: 'acme', sigId: 's-acme' } },
  );

  return {
    id: 'moved-subtree-rejects-new-parent',
    title: '签名子树被搬动到未授权租户：parent 面独立拒绝',
    input: {
      documents: [origFeatDoc()],
      branches: [{ branchId: 'moved', priority: 10, doc: moved }],
      signatures: [{ sigId: 's-acme', issuer: 'acme', docId: 'orig', nodeId: 'feat-root' }],
      grants: [
        // acme may decide name/value/existence at globex but NOT parent.
        { issuer: 'acme', path: '/tenants/globex/**', facets: ['name', 'value', 'existence'] },
      ],
    },
    expect: {
      ok: false,
      errorIncludes: [
        { code: 'UNAUTHORIZED_FACET', pointer: '/tenants/globex/features/feat/toggle', facet: 'parent' },
        { code: 'UNAUTHORIZED_FACET', pointer: '/tenants/globex/features/feat/limit', facet: 'parent' },
      ],
    },
  };
}

function movedAllow(): CanonicalScenario {
  const moved = doc(
    'moved-ok',
    obj({
      tenants: obj({
        acme: obj({
          features: obj({
            feat: obj(
              { toggle: leaf(true, { id: 'feat-toggle' }), limit: leaf(3, { id: 'feat-limit' }) },
              { id: 'feat-root', issuer: 'acme', sig: 's-acme' },
            ),
          }),
        }),
      }),
    }),
    { rootId: 'm-root', attribution: { issuer: 'acme', sigId: 's-acme' } },
  );

  return {
    id: 'moved-subtree-allowed-location',
    title: '签名子树在授权位置内搬动可发布且追溯 moved',
    input: {
      documents: [origFeatDoc()],
      branches: [{ branchId: 'moved', priority: 10, doc: moved }],
      signatures: [{ sigId: 's-acme', issuer: 'acme', docId: 'orig', nodeId: 'feat-root' }],
      grants: [all('acme', '/tenants/acme/**')],
    },
    expect: {
      ok: true,
      tree: { tenants: { acme: { features: { feat: { toggle: true, limit: 3 } } } } },
      leafChecks: [
        { pointer: '/tenants/acme/features/feat/toggle', viaIncludes: ['moved'], authorized: true },
        { pointer: '/tenants/acme/features/feat/limit', viaIncludes: ['moved'], authorized: true },
      ],
    },
  };
}

function movedInjectedChild(): CanonicalScenario {
  const moved = doc(
    'moved-inject',
    obj({
      tenants: obj({
        acme: obj({
          feat: obj(
            {
              toggle: leaf(true, { id: 'feat-toggle', issuer: 'acme', sig: 's-acme' }),
              extra: leaf('pwn', { id: 'feat-extra', issuer: 'acme', sig: 's-acme' }),
            },
            { id: 'feat-root', issuer: 'acme', sig: 's-acme' },
          ),
        }),
      }),
    }),
    { rootId: 'mi-root' },
  );

  return {
    id: 'moved-subtree-new-child-not-covered',
    title: '搬动后注入的新后代不在原签名范围：SIGNATURE_SCOPE',
    input: {
      documents: [origFeatDoc()],
      branches: [{ branchId: 'moved', priority: 10, doc: moved }],
      signatures: [{ sigId: 's-acme', issuer: 'acme', docId: 'orig', nodeId: 'feat-root' }],
      grants: [all('acme', '/tenants/acme/**')],
    },
    expect: {
      ok: false,
      errorIncludes: [{ code: 'SIGNATURE_SCOPE', pointer: '/tenants/acme/feat/extra' }],
    },
  };
}

/* ------------------------------------------------------------------ */
/* 2. Same key name under different parents                           */
/* ------------------------------------------------------------------ */

function sameNameDifferentParents(): CanonicalScenario {
  const tree = doc(
    'sn',
    obj({
      a: obj({ name: leaf('A', { id: 'name-a', issuer: 'acme', sig: 's-a' }) }, { id: 'node-a' }),
      b: obj({ name: leaf('B', { id: 'name-b', issuer: 'globex', sig: 's-g' }) }, { id: 'node-b' }),
    }),
    { rootId: 'sn-root' },
  );

  return {
    id: 'same-name-different-parents',
    title: '同名键位于不同父节点：位置与授权分别追溯',
    input: {
      branches: [{ branchId: 'main', priority: 10, doc: tree }],
      signatures: [
        { sigId: 's-a', issuer: 'acme', docId: 'sn', nodeId: 'node-a' },
        { sigId: 's-g', issuer: 'globex', docId: 'sn', nodeId: 'node-b' },
      ],
      grants: [all('acme', '/a/**')],
    },
    expect: {
      ok: false,
      tree: { a: { name: 'A' }, b: { name: 'B' } },
      errorIncludes: [{ code: 'UNAUTHORIZED_FACET', pointer: '/b/name' }],
      leafChecks: [{ pointer: '/a/name', authorized: true, value: 'A' }],
    },
  };
}

/* ------------------------------------------------------------------ */
/* 3. Cross-scope tombstone + same-scope control                      */
/* ------------------------------------------------------------------ */

function crossScopeTombstone(): CanonicalScenario {
  const secure = doc(
    'sec',
    obj({ db: obj({ password: leaf('secret', { id: 'pw' }) }, { id: 'db-sec' }) }),
    { rootId: 'sec-root', attribution: { issuer: 'secure', sigId: 's-sec' } },
  );
  const ops = doc(
    'ops',
    obj({ db: obj({ password: tomb({ id: 'pw-tomb' }) }, { id: 'db-ops' }) }),
    { rootId: 'ops-root', attribution: { issuer: 'ops', sigId: 's-ops' } },
  );

  return {
    id: 'cross-scope-tombstone-blocked',
    title: '跨供应商作用域墓碑不能静默清除高优先级保护键',
    input: {
      branches: [
        { branchId: 'secure', priority: 50, doc: secure },
        { branchId: 'ops', priority: 5, doc: ops },
      ],
      signatures: [
        { sigId: 's-sec', issuer: 'secure', docId: 'sec', nodeId: 'sec-root' },
        { sigId: 's-ops', issuer: 'ops', docId: 'ops', nodeId: 'ops-root' },
      ],
      grants: [all('secure', '/db/**'), all('ops', '/db/**')],
      protectedKeys: [{ path: '/db/password', owner: 'secure' }],
    },
    expect: {
      ok: false,
      errorIncludes: [{ code: 'CROSS_SCOPE_TOMBSTONE', pointer: '/db/password' }],
    },
  };
}

function sameScopeTombstone(): CanonicalScenario {
  const secure = doc(
    'sec2',
    obj({ db: obj({ old: tomb({ id: 'old-tomb' }) }, { id: 'db-sec2' }) }),
    { rootId: 'sec2-root', attribution: { issuer: 'secure', sigId: 's-sec2' } },
  );

  return {
    id: 'same-scope-tombstone-allowed',
    title: '同作用域授权墓碑可删除且不产生叶',
    input: {
      branches: [{ branchId: 'secure', priority: 50, doc: secure }],
      signatures: [{ sigId: 's-sec2', issuer: 'secure', docId: 'sec2', nodeId: 'sec2-root' }],
      grants: [all('secure', '/db/**')],
      protectedKeys: [{ path: '/db/old', owner: 'secure' }],
    },
    expect: { ok: true, tree: { db: {} } },
  };
}

/* ------------------------------------------------------------------ */
/* 4. Delete then re-inherit / exposure + revocation                  */
/* ------------------------------------------------------------------ */

function deleteReinherit(revokeExposed = false): CanonicalScenario {
  const base = doc('base', obj({ theme: leaf('classic', { id: 'theme-base' }) }), {
    rootId: 'base-root',
    attribution: { issuer: 'base', sigId: 's-base' },
  });
  const dark = doc('dark', obj({ theme: leaf('dark', { id: 'theme-dark' }) }), {
    rootId: 'dark-root',
    attribution: { issuer: 'theme-vendor', sigId: 's-theme' },
  });

  return {
    id: revokeExposed ? 'delete-exposes-revoked-source' : 'delete-then-reinherit',
    title: revokeExposed
      ? '删除覆盖键后暴露的分支值签名已撤销：拒绝'
      : '删除低优先级签名覆盖键后重新继承另一分支值并追溯暴露',
    input: {
      branches: [
        { branchId: 'dark', priority: 20, doc: dark },
        { branchId: 'base', priority: 10, doc: base },
      ],
      deletions: [{ path: '/theme', branchId: 'dark', issuer: 'admin', sigId: 's-admin' }],
      signatures: [
        { sigId: 's-base', issuer: 'base', docId: 'base', nodeId: 'base-root' },
        { sigId: 's-theme', issuer: 'theme-vendor', docId: 'dark', nodeId: 'dark-root' },
        { sigId: 's-admin', issuer: 'admin', docId: 'admin-doc', nodeId: 'admin-root' },
      ],
      revocations: revokeExposed ? ['s-base'] : [],
      grants: [all('base', '/**'), { issuer: 'admin', path: '/theme', facets: ['existence'] }],
    },
    expect: revokeExposed
      ? { ok: false, errorIncludes: [{ code: 'SIGNATURE_REVOKED', pointer: '/theme' }] }
      : {
          ok: true,
          tree: { theme: 'classic' },
          leafChecks: [{ pointer: '/theme', value: 'classic', branchId: 'base', exposed: true }],
        },
  };
}

/* ------------------------------------------------------------------ */
/* 5. Shared template across tenants / roles                          */
/* ------------------------------------------------------------------ */

function templateDocs() {
  const web = doc(
    'tpl-web',
    obj({ replicas: leaf(2, { id: 'tpl-replicas' }), mode: leaf('static', { id: 'tpl-mode' }) }, {
      id: 'tpl-root',
    }),
    { rootId: 'tpl-root', attribution: { issuer: 'tpl-team', sigId: 's-tpl' } },
  );
  return { web };
}

function tenantTree(): ReturnType<typeof doc> {
  return doc(
    'tenants',
    obj({
      tenants: obj({
        acme: obj({
          web: ref('web', {
            id: 'ref-acme',
            issuer: 'deploy-acme',
            sig: 's-dep-acme',
            tenant: 'acme',
            role: 'admin',
          }),
        }),
        globex: obj({
          web: ref('web', {
            id: 'ref-globex',
            issuer: 'deploy-globex',
            sig: 's-dep-globex',
            tenant: 'globex',
            role: 'viewer',
          }),
        }),
      }),
    }),
    { rootId: 't-root' },
  );
}

const templateSignatures = [
  { sigId: 's-tpl', issuer: 'tpl-team' as const, docId: 'tpl-web', nodeId: 'tpl-root' },
  { sigId: 's-dep-acme', issuer: 'deploy-acme' as const, docId: 'dep-acme', nodeId: 'dep-root' },
  { sigId: 's-dep-globex', issuer: 'deploy-globex' as const, docId: 'dep-globex', nodeId: 'dep-root' },
];

function templateCrossTenantReject(): CanonicalScenario {
  return {
    id: 'shared-template-cross-tenant-isolated',
    title: '共享模板跨租户/角色展开：acme 授权不延伸到 globex',
    input: {
      branches: [{ branchId: 'tenants', priority: 10, doc: tenantTree() }],
      templates: templateDocs(),
      signatures: templateSignatures,
      grants: [
        all('deploy-acme', '/tenants/acme/**'),
        // globex deployer is only allowed for admin role; site expands as viewer.
        { issuer: 'deploy-globex', path: '/tenants/globex/**', facets: '*', role: 'admin' },
      ],
    },
    expect: {
      ok: false,
      errorIncludes: [{ code: 'UNAUTHORIZED_FACET', pointer: '/tenants/globex/web/replicas' }],
    },
  };
}

function templateCrossTenantAllow(): CanonicalScenario {
  return {
    id: 'shared-template-cross-tenant-allowed',
    title: '同一共享模板在两个授权租户下展开',
    input: {
      branches: [{ branchId: 'tenants', priority: 10, doc: tenantTree() }],
      templates: templateDocs(),
      signatures: templateSignatures,
      grants: [all('deploy-acme', '/tenants/acme/**'), all('deploy-globex', '/tenants/globex/**')],
    },
    expect: {
      ok: true,
      tree: {
        tenants: {
          acme: { web: { replicas: 2, mode: 'static' } },
          globex: { web: { replicas: 2, mode: 'static' } },
        },
      },
      leafChecks: [
        { pointer: '/tenants/acme/web/replicas', viaIncludes: ['template'], authorized: true },
        { pointer: '/tenants/globex/web/mode', viaIncludes: ['template'], authorized: true },
      ],
    },
  };
}

function templateCycle(): CanonicalScenario {
  const a = doc('tpl-a', obj({ next: ref('b', { id: 'ref-a-b' }) }, { id: 'a-root' }), { rootId: 'a-root' });
  const b = doc('tpl-b', obj({ next: ref('a', { id: 'ref-b-a' }) }, { id: 'b-root' }), { rootId: 'b-root' });
  const main = doc('cyc', obj({ x: ref('a', { id: 'ref-start' }) }), { rootId: 'cyc-root' });

  return {
    id: 'template-recursion-cycle-located',
    title: '模板递归环有确定位置并失败零发布',
    input: {
      branches: [{ branchId: 'main', priority: 10, doc: main }],
      templates: { a, b },
      options: { requireSignatureEvidence: false },
      grants: [{ issuer: '*', path: '/**', facets: '*' }],
    },
    expect: {
      ok: false,
      errorIncludes: [{ code: 'TEMPLATE_CYCLE' }],
    },
  };
}

/* ------------------------------------------------------------------ */
/* 6. Explicit null vs missing vs tombstone                           */
/* ------------------------------------------------------------------ */

function nullOverride(): CanonicalScenario {
  const low = doc(
    'low',
    obj({
      nullWins: obj({ key: leaf('low') }),
      missingCase: obj({ key: leaf('low') }),
      tombCase: obj({ key: leaf('low') }),
    }),
    { rootId: 'low-root', attribution: { issuer: 'cfg', sigId: 's-low' } },
  );
  const high = doc(
    'high',
    obj({
      nullWins: obj({ key: nil({ id: 'n-null' }) }),
      // missingCase intentionally absent -> missing -> lower value survives
      tombCase: obj({ key: tomb({ id: 'n-tomb' }) }),
    }),
    { rootId: 'high-root', attribution: { issuer: 'cfg', sigId: 's-high' } },
  );

  return {
    id: 'null-override-three-states',
    title: '显式空值覆盖；缺失透传；墓碑删除',
    input: {
      branches: [
        { branchId: 'high', priority: 20, doc: high },
        { branchId: 'low', priority: 10, doc: low },
      ],
      signatures: [
        { sigId: 's-low', issuer: 'cfg', docId: 'low', nodeId: 'low-root' },
        { sigId: 's-high', issuer: 'cfg', docId: 'high', nodeId: 'high-root' },
      ],
      grants: [all('cfg', '/**')],
    },
    expect: {
      ok: true,
      tree: {
        nullWins: { key: null },
        missingCase: { key: 'low' },
        tombCase: {},
      },
      leafChecks: [
        { pointer: '/nullWins/key', value: null },
        { pointer: '/missingCase/key', value: 'low' },
      ],
    },
  };
}

/* ------------------------------------------------------------------ */
/* Alias collision                                                    */
/* ------------------------------------------------------------------ */

function aliasCollision(): CanonicalScenario {
  const tree = doc(
    'al',
    obj({ user: obj({ 'user-name': leaf('legacy', { id: 'un-legacy' }), userName: leaf('canonical', { id: 'un-canon' }) }, { id: 'user-obj' }) }),
    { rootId: 'al-root', attribution: { issuer: 'cfg', sigId: 's-al' } },
  );

  return {
    id: 'alias-collision-located',
    title: '别名折叠碰撞有确定位置并失败',
    input: {
      branches: [{ branchId: 'main', priority: 10, doc: tree }],
      signatures: [{ sigId: 's-al', issuer: 'cfg', docId: 'al', nodeId: 'al-root' }],
      grants: [all('cfg', '/**'), all('namer', '/**', { facets: ['name'] })],
      aliases: [
        {
          alias: 'user-name',
          canonical: 'userName',
          at: '/user',
          attribution: { issuer: 'namer', sigId: 's-namer' },
        },
      ],
    },
    expect: {
      ok: false,
      errorIncludes: [{ code: 'ALIAS_COLLISION', pointer: '/user', facet: 'name' }],
    },
  };
}

export const CANONICAL_SCENARIOS: CanonicalScenario[] = [
  movedReject(),
  movedAllow(),
  movedInjectedChild(),
  sameNameDifferentParents(),
  crossScopeTombstone(),
  sameScopeTombstone(),
  deleteReinherit(false),
  deleteReinherit(true),
  templateCrossTenantReject(),
  templateCrossTenantAllow(),
  templateCycle(),
  nullOverride(),
  aliasCollision(),
];
