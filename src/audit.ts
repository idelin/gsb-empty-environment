import type { PdfDocument } from './parser.ts';
import type {
  AuditFinding,
  PdfDict,
  PdfObject,
  PdfRef,
  SignatureInfo,
  XrefEntry,
} from './types.ts';

/** Object roles derived from the signed snapshot's reachable graph. */
export type ObjectRole =
  | 'root'
  | 'page-tree'
  | 'page'
  | 'content'
  | 'resource'
  | 'font'
  | 'acroform'
  | 'field'
  | 'annotation'
  | 'other';

interface RoleNode {
  num: number;
  role: ObjectRole;
  /** Dictionary key via which this object was reached (best effort). */
  edgeKey: string | null;
}

function isRef(obj: PdfObject | undefined): obj is PdfRef {
  return !!obj && obj.kind === 'ref';
}

function asDict(obj: PdfObject | null): PdfDict | null {
  if (!obj) return null;
  if (obj.kind === 'dict') return obj;
  if (obj.kind === 'stream') return obj.dict;
  return null;
}

function nameOf(obj: PdfObject | undefined): string | null {
  return obj && obj.kind === 'name' ? obj.value : null;
}

function refsIn(obj: PdfObject | undefined): PdfRef[] {
  if (!obj) return [];
  if (obj.kind === 'ref') return [obj];
  if (obj.kind === 'array') return obj.items.flatMap((item) => refsIn(item));
  if (obj.kind === 'dict') return [...obj.entries.values()].flatMap((value) => refsIn(value));
  if (obj.kind === 'stream') return refsIn(obj.dict);
  return [];
}

function refsInKeyed(obj: PdfObject | undefined): Array<{ ref: PdfRef; key: string | null }> {
  if (!obj) return [];
  if (obj.kind === 'ref') return [{ ref: obj, key: null }];
  if (obj.kind === 'array') return obj.items.flatMap((item) => refsInKeyed(item));
  if (obj.kind === 'dict') {
    return [...obj.entries.entries()].flatMap(([key, value]) =>
      refsInKeyed(value).map((entry) => ({ ref: entry.ref, key })),
    );
  }
  if (obj.kind === 'stream') return refsInKeyed(obj.dict);
  return [];
}

function deepEqual(a: PdfObject | undefined, b: PdfObject | undefined): boolean {
  if (a === b) return true;
  if (!a || !b || a.kind !== b.kind) return false;
  switch (a.kind) {
    case 'ref':
      return b.kind === 'ref' && a.num === b.num && a.gen === b.gen;
    case 'null':
      return b.kind === 'null';
    case 'bool':
      return b.kind === 'bool' && a.value === b.value;
    case 'num':
      return b.kind === 'num' && a.value === b.value;
    case 'name':
      return b.kind === 'name' && a.value === b.value;
    case 'string':
      return b.kind === 'string' && bytesEqual(a.value, b.value);
    case 'array':
      return b.kind === 'array' && a.items.length === b.items.length &&
        a.items.every((item, index) => deepEqual(item, b.items[index]));
    case 'dict':
      return b.kind === 'dict' && dictEqual(a, b);
    case 'stream':
      return b.kind === 'stream' && dictEqual(a.dict, b.dict) && bytesEqual(a.raw, b.raw);
  }
}

function dictEqual(a: PdfDict, b: PdfDict): boolean {
  if (a.entries.size !== b.entries.size) return false;
  for (const [key, value] of a.entries) {
    if (!deepEqual(value, b.entries.get(key))) return false;
  }
  return true;
}

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/** Build the reachable object graph + role labels for one revision snapshot. */
export function buildRoleGraph(
  doc: PdfDocument,
  revisionIndex: number,
): Map<number, RoleNode> {
  const revision = doc.revisions[revisionIndex];
  const rootRef = revision.trailer.entries.get('Root');
  const roles = new Map<number, RoleNode>();
  if (!isRef(rootRef)) return roles;

  const queue: RoleNode[] = [{ num: rootRef.num, role: 'root', edgeKey: null }];
  const visited = new Set<number>();
  while (queue.length > 0) {
    const node = queue.shift() as RoleNode;
    if (visited.has(node.num)) {
      const existing = roles.get(node.num);
      if (existing && roleRank(node.role) < roleRank(existing.role)) existing.role = node.role;
      continue;
    }
    visited.add(node.num);
    roles.set(node.num, node);
    const dict = asDict(doc.getObject(node.num, revisionIndex));
    if (!dict) continue;
    for (const [key, value] of dict.entries) {
      for (const { ref } of refsInKeyed(value)) {
        queue.push({ num: ref.num, role: childRole(node.role, key, dict), edgeKey: key });
      }
    }
  }
  return roles;
}

function roleRank(role: ObjectRole): number {
  const order: ObjectRole[] = [
    'other', 'font', 'resource', 'annotation', 'field', 'acroform',
    'content', 'page', 'page-tree', 'root',
  ];
  return order.indexOf(role);
}

function childRole(parent: ObjectRole, key: string, parentDict: PdfDict): ObjectRole {
  if (key === 'Root') return 'root';
  if (key === 'DSS') return 'other';
  if (key === 'AcroForm') return 'acroform';
  if (key === 'Fields') return parent === 'acroform' ? 'field' : parent;
  if (key === 'Annots') return 'annotation';
  if (key === 'Parent') return parent;
  if (key === 'Kids') return parent === 'root' || parent === 'page-tree' ? 'page-tree' : parent;
  if (key === 'Contents' && (parent === 'page' || parent === 'page-tree')) return 'content';
  if (key === 'Resources') return 'resource';
  if (key === 'Font') return 'font';
  if (key === 'P') return 'page';
  const type = nameOf(parentDict.entries.get('Type'));
  if (key === 'Kids' || (type === 'Pages' && key !== 'Resources')) return 'page-tree';
  if (type === 'Page') return 'page';
  return parent === 'other' ? 'other' : parent;
}

interface AuditState {
  doc: PdfDocument;
  findings: AuditFinding[];
  signature: SignatureInfo;
  signedIndex: number;
  latestIndex: number;
  roles: Map<number, RoleNode>;
  baselineNums: Set<number>;
  redefined: Set<number>;
  freed: Set<number>;
  newInUse: Set<number>;
  allowedNew: Set<number>;
  newSigNums: Set<number>;
  dssNums: Set<number>;
}

/**
 * Default-deny audit of every incremental update appended after the bytes
 * covered by `signature`. Returns the findings (empty array == clean).
 */
export function auditIncremental(
  doc: PdfDocument,
  signature: SignatureInfo,
): AuditFinding[] {
  const findings: AuditFinding[] = [];
  if (signature.signingRevisionIndex < 0) {
    findings.push({
      code: 'AUDIT_NO_SNAPSHOT',
      severity: 'error',
      message: 'cannot audit without a resolved signing revision',
      objectNumber: signature.objectNumber,
    });
    return findings;
  }

  const signedIndex = signature.signingRevisionIndex;
  const latestIndex = doc.revisions.length - 1;
  if (signedIndex === latestIndex) return findings;

  const signedRevision = doc.revisions[signedIndex];
  const baselineNums = new Set<number>();
  for (const [num, entry] of signedRevision.entries) {
    if (entry.type !== 0) baselineNums.add(num);
  }

  const redefined = new Set<number>();
  const freed = new Set<number>();
  const newInUse = new Set<number>();
  for (let rev = signedIndex + 1; rev <= latestIndex; rev += 1) {
    for (const [num, entry] of doc.revisions[rev].entries) {
      if (baselineNums.has(num)) {
        const before = signedRevision.entries.get(num) as XrefEntry;
        if (entry.type === 0) {
          freed.add(num);
        } else if (!sameEntry(before, entry)) {
          redefined.add(num);
        }
      } else if (entry.type !== 0) {
        newInUse.add(num);
      }
    }
  }

  const roles = buildRoleGraph(doc, signedIndex);
  const state: AuditState = {
    doc,
    findings,
    signature,
    signedIndex,
    latestIndex,
    roles,
    baselineNums,
    redefined,
    freed,
    newInUse,
    allowedNew: new Set(),
    newSigNums: new Set(),
    dssNums: new Set(),
  };

  checkTrailerIntegrity(state);
  checkFreedObjects(state);
  classifyAllowedNewRoots(state);
  checkRedefinedBaselineObjects(state);
  walkAllowedSubtrees(state);
  checkUnapprovedNewObjects(state);
  return findings;
}

function sameEntry(a: XrefEntry, b: XrefEntry): boolean {
  return a.type === b.type && a.offset === b.offset && a.gen === b.gen &&
    (a.streamIndex ?? 0) === (b.streamIndex ?? 0);
}

function report(state: AuditState, code: string, message: string, num?: number): void {
  state.findings.push({
    code,
    severity: 'error',
    message,
    objectNumber: num,
    revisionIndex: state.latestIndex,
  });
}

function checkTrailerIntegrity(state: AuditState): void {
  const signed = state.doc.revisions[state.signedIndex].trailer;
  const latest = state.doc.revisions[state.latestIndex].trailer;
  const rootA = signed.entries.get('Root');
  const rootB = latest.entries.get('Root');
  if (!deepEqual(rootA, rootB)) {
    report(state, 'SHADOW_ROOT_SWAP', 'trailer /Root object reference was replaced');
  }
  const protectedKeys = ['Encrypt', 'Info', 'ID'];
  for (const key of protectedKeys) {
    if (!deepEqual(signed.entries.get(key), latest.entries.get(key))) {
      report(state, 'TRAILER_KEY_CHANGED', `trailer key /${key} changed after signing`);
    }
  }
  if (latest.entries.has('Size')) {
    const sizeA = signed.entries.get('Size');
    const sizeB = latest.entries.get('Size');
    if (sizeA && sizeB && sizeA.kind === 'num' && sizeB.kind === 'num' &&
      sizeB.value < sizeA.value) {
      report(state, 'TRAILER_SIZE_SHRUNK', 'trailer /Size shrunk after signing');
    }
  }
}

function checkFreedObjects(state: AuditState): void {
  for (const num of state.freed) {
    if (state.roles.has(num)) {
      report(state, 'SHADOW_OBJECT_FREED', `reachable object ${num} was deleted after signing`, num);
    }
  }
}

/** Identify new signature fields and the /DSS subtree as permitted new roots. */
function classifyAllowedNewRoots(state: AuditState): void {
  const rootNum = rootObjectNumber(state);
  if (rootNum === null) return;
  const rootDict = asDict(state.doc.getObject(rootNum, state.latestIndex));
  if (!rootDict) return;

  const dss = rootDict.entries.get('DSS');
  if (isRef(dss) && state.newInUse.has(dss.num)) {
    state.dssNums.add(dss.num);
    state.allowedNew.add(dss.num);
  }

  const acroRef = rootDict.entries.get('AcroForm');
  if (!isRef(acroRef)) return;
  const acroDict = asDict(state.doc.getObject(acroRef.num, state.latestIndex));
  if (!acroDict) return;
  const fields = acroDict.entries.get('Fields');
  if (!fields || fields.kind !== 'array') return;
  for (const item of fields.items) {
    if (!isRef(item)) continue;
    if (!state.newInUse.has(item.num)) continue;
    const dict = asDict(state.doc.getObject(item.num, state.latestIndex));
    if (!dict) continue;
    const ft = nameOf(dict.entries.get('FT'));
    const type = nameOf(dict.entries.get('Type'));
    if (ft === 'Sig' || type === 'Sig' || type === 'DocTimeStamp') {
      state.newSigNums.add(item.num);
      state.allowedNew.add(item.num);
    } else {
      report(
        state,
        'UNAPPROVED_NEW_FIELD',
        `non-signature field ${item.num} appended to /AcroForm /Fields`,
        item.num,
      );
    }
  }
}

function rootObjectNumber(state: AuditState): number | null {
  const root = state.doc.revisions[state.signedIndex].trailer.entries.get('Root');
  return isRef(root) ? root.num : null;
}

/** Audit every pre-existing object that a later xref table redefines. */
function checkRedefinedBaselineObjects(state: AuditState): void {
  for (const num of state.redefined) {
    const roleNode = state.roles.get(num);
    const role = roleNode?.role ?? 'other';
    const oldObj = state.doc.getObject(num, state.signedIndex);
    const newObj = state.doc.getObject(num, state.latestIndex);

    if (role === 'root') {
      auditRootRedefinition(state, num, oldObj, newObj);
      continue;
    }
    if (role === 'acroform') {
      auditAcroFormRedefinition(state, num, oldObj, newObj);
      continue;
    }
    if (role === 'page') {
      auditPageRedefinition(state, num, oldObj, newObj);
      continue;
    }
    if (role === 'page-tree' || role === 'content' || role === 'resource' ||
      role === 'font' || role === 'annotation' || role === 'field') {
      report(
        state,
        `SHADOW_${role.toUpperCase()}_MODIFIED`,
        `protected ${role} object ${num} was redefined after signing`,
        num,
      );
      continue;
    }
    if (deepEqual(oldObj, newObj)) continue;
    report(
      state,
      'UNAPPROVED_BASELINE_CHANGE',
      `signed object ${num} was modified without an allowed reason`,
      num,
    );
  }
}

function auditRootRedefinition(
  state: AuditState,
  num: number,
  oldObj: PdfObject | null,
  newObj: PdfObject | null,
): void {
  const oldDict = asDict(oldObj);
  const newDict = asDict(newObj);
  if (!oldDict || !newDict) {
    report(state, 'SHADOW_ROOT_MODIFIED', `root object ${num} changed type`, num);
    return;
  }
  for (const [key, value] of oldDict.entries) {
    if (!deepEqual(value, newDict.entries.get(key))) {
      report(state, 'SHADOW_ROOT_MODIFIED', `root /${key} was modified after signing`, num);
      return;
    }
  }
  for (const key of newDict.entries.keys()) {
    if (oldDict.entries.has(key)) continue;
    if (key === 'DSS') {
      const dss = newDict.entries.get(key);
      if (isRef(dss) && state.newInUse.has(dss.num)) {
        state.dssNums.add(dss.num);
        state.allowedNew.add(dss.num);
      }
      continue;
    }
    if (key === 'AcroForm') {
      const acro = newDict.entries.get(key);
      if (isRef(acro) && state.newInUse.has(acro.num)) {
        state.allowedNew.add(acro.num);
        continue;
      }
    }
    report(state, 'SHADOW_ROOT_MODIFIED', `root gained unexpected key /${key}`, num);
  }
}

function auditAcroFormRedefinition(
  state: AuditState,
  num: number,
  oldObj: PdfObject | null,
  newObj: PdfObject | null,
): void {
  const oldDict = asDict(oldObj);
  const newDict = asDict(newObj);
  if (!oldDict || !newDict) {
    report(state, 'SHADOW_ACROFORM_MODIFIED', `AcroForm ${num} changed type`, num);
    return;
  }
  for (const [key, value] of oldDict.entries) {
    if (key === 'Fields') continue;
    if (key === 'SigFlags') {
      const next = newDict.entries.get(key);
      if (next && next.kind === 'num' && (next.value === value || next.value === 3)) continue;
      report(state, 'SHADOW_ACROFORM_MODIFIED', 'AcroForm /SigFlags changed illegally', num);
      return;
    }
    if (!deepEqual(value, newDict.entries.get(key))) {
      report(state, 'SHADOW_ACROFORM_MODIFIED', `AcroForm /${key} changed after signing`, num);
      return;
    }
  }
  const oldFields = oldDict.entries.get('Fields');
  const newFields = newDict.entries.get('Fields');
  if (!oldFields || oldFields.kind !== 'array' || !newFields || newFields.kind !== 'array') {
    report(state, 'SHADOW_ACROFORM_MODIFIED', 'AcroForm /Fields missing or invalid', num);
    return;
  }
  if (newFields.items.length < oldFields.items.length) {
    report(state, 'SHADOW_ACROFORM_MODIFIED', 'AcroForm /Fields lost entries', num);
    return;
  }
  for (let i = 0; i < oldFields.items.length; i += 1) {
    if (!deepEqual(oldFields.items[i], newFields.items[i])) {
      report(state, 'SHADOW_ACROFORM_MODIFIED', 'existing AcroForm field was replaced', num);
      return;
    }
  }
  for (const added of newFields.items.slice(oldFields.items.length)) {
    if (!isRef(added)) {
      report(state, 'SHADOW_ACROFORM_MODIFIED', 'new AcroForm field must be an indirect reference', num);
      continue;
    }
    if (!state.newInUse.has(added.num)) {
      report(state, 'SHADOW_ACROFORM_MODIFIED', 'appended field reuses a signed object', num);
      continue;
    }
    state.allowedNew.add(added.num);
    state.newSigNums.add(added.num);
  }
}

function auditPageRedefinition(
  state: AuditState,
  num: number,
  oldObj: PdfObject | null,
  newObj: PdfObject | null,
): void {
  const oldDict = asDict(oldObj);
  const newDict = asDict(newObj);
  if (!oldDict || !newDict) {
    report(state, 'SHADOW_PAGE_MODIFIED', `page ${num} changed type`, num);
    return;
  }
  for (const [key, value] of oldDict.entries) {
    if (key === 'Annots') continue;
    if (!deepEqual(value, newDict.entries.get(key))) {
      report(state, 'SHADOW_PAGE_MODIFIED', `page /${key} changed after signing`, num);
      return;
    }
  }
  for (const key of newDict.entries.keys()) {
    if (!oldDict.entries.has(key) && key !== 'Annots') {
      report(state, 'SHADOW_PAGE_MODIFIED', `page gained unexpected key /${key}`, num);
      return;
    }
  }
  const oldAnnots = oldDict.entries.get('Annots');
  const newAnnots = newDict.entries.get('Annots');
  const oldList = oldAnnots && oldAnnots.kind === 'array' ? oldAnnots.items : [];
  const newList = newAnnots && newAnnots.kind === 'array' ? newAnnots.items : [];
  if (newList.length < oldList.length) {
    report(state, 'SHADOW_ANNOTS_MODIFIED', 'page /Annots lost entries (Hide attack)', num);
    return;
  }
  for (let i = 0; i < oldList.length; i += 1) {
    if (!deepEqual(oldList[i], newList[i])) {
      report(state, 'SHADOW_ANNOTS_MODIFIED', 'existing annotation was replaced', num);
      return;
    }
  }
  for (const added of newList.slice(oldList.length)) {
    if (!isRef(added) || !state.newInUse.has(added.num)) {
      report(state, 'SHADOW_ANNOTS_MODIFIED', 'new annotation must be a new indirect object', num);
      continue;
    }
    state.allowedNew.add(added.num);
  }
}

/**
 * Traverse every permitted new subtree (new signature fields, their widget
 * annotations, appearance streams and the /DSS tree). Any new object reached
 * here must satisfy a strict shape; references into signed visual objects are
 * only allowed through the documented back-pointers (/P, /AcroForm).
 */
function walkAllowedSubtrees(state: AuditState): void {
  const queue = [...state.allowedNew];
  const seen = new Set<number>();
  while (queue.length > 0) {
    const num = queue.shift() as number;
    if (seen.has(num)) continue;
    seen.add(num);
    if (state.baselineNums.has(num)) continue;
    const obj = state.doc.getObject(num, state.latestIndex);
    const dict = asDict(obj);
    if (!dict) {
      report(state, 'UNAPPROVED_NEW_OBJECT', `new object ${num} is not a dictionary`, num);
      continue;
    }

    const isSignature = state.newSigNums.has(num);
    const isDss = state.dssNums.has(num);
    if (isSignature) validateNewSignatureShape(state, num, dict);
    if (isDss) validateDssShape(state, num, dict);

    for (const { ref, key } of refsInKeyed(dict)) {
      if (state.baselineNums.has(ref.num)) {
        if (key === 'P' || key === 'AcroForm' || key === 'Parent') continue;
        report(
          state,
          'SHADOW_NEW_REFERENCE',
          `new object ${num} references signed object ${ref.num} via /${key ?? '?'}`,
          num,
        );
        continue;
      }
      if (!state.newInUse.has(ref.num)) continue;
      if (isDss) state.dssNums.add(ref.num);
      if (isSignature && (key === 'AP' || key === 'V' || key === 'Parent')) {
        if (key === 'V') state.newSigNums.add(ref.num);
      }
      state.allowedNew.add(ref.num);
      queue.push(ref.num);
    }
  }
}

function validateNewSignatureShape(
  state: AuditState,
  num: number,
  dict: PdfDict,
): void {
  const ft = nameOf(dict.entries.get('FT'));
  const type = nameOf(dict.entries.get('Type'));
  const looksLikeSig = ft === 'Sig' || type === 'Sig' || type === 'DocTimeStamp' ||
    dict.entries.has('ByteRange') || dict.entries.has('Contents');
  const looksLikeWidget = type === 'Annot' || ft === 'Sig' || dict.entries.has('Subtype') ||
    dict.entries.has('Rect');
  if (!looksLikeSig && !looksLikeWidget) {
    report(state, 'UNAPPROVED_NEW_OBJECT', `new field ${num} is not a signature widget`, num);
  }
  const forbidden = ['Contents_stream', 'Kids'];
  for (const key of forbidden) {
    if (key === 'Kids' && dict.entries.has('Kids')) {
      report(state, 'SHADOW_NEW_REFERENCE', `new field ${num} must not introduce /Kids`, num);
    }
  }
  const subtype = nameOf(dict.entries.get('Subtype'));
  if (subtype && subtype !== 'Widget') {
    report(state, 'UNAPPROVED_NEW_OBJECT', `new annotation ${num} subtype must be /Widget`, num);
  }
}

function validateDssShape(state: AuditState, num: number, dict: PdfDict): void {
  const allowedKeys = new Set([
    'Type', 'VRI', 'CRLs', 'OCSPs', 'Certs',
  ]);
  const streamHeaderKeys = new Set(['Length', 'Filter', 'DecodeParms']);
  for (const key of dict.entries.keys()) {
    if (!allowedKeys.has(key) && !streamHeaderKeys.has(key)) {
      report(state, 'DSS_INVALID_SHAPE', `/DSS object ${num} contains forbidden key /${key}`, num);
    }
  }
}

/** Default-deny sweep: every new in-use object must belong to an allowed tree. */
function checkUnapprovedNewObjects(state: AuditState): void {
  for (const num of state.newInUse) {
    if (state.allowedNew.has(num)) continue;
    report(
      state,
      'UNAPPROVED_NEW_REACHABLE',
      `object ${num} added after signing is outside signature/DSS whitelist`,
      num,
    );
  }
}
