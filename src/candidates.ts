import type {
  CandidateGroup,
  PatchCandidate,
  PropertySchema,
  RootSchema,
  ValidationError,
} from './types.ts';
import { getAtPath, isPlainObject, joinPtr, parsePtr } from './paths.ts';

export function errorKey(err: ValidationError): string {
  return `${err.kind}|${err.instancePath}|${err.rulePath}|${err.op ?? ''}|${err.otherPath ?? ''}`;
}

// ---- 代价表（确定性建议便宜；需人工选择/有损转换更贵） ----
export const COST = {
  coerceNumberFromNumber: 5,   // 数字->integer 截断/四舍五入（需人工选择具体方式）
  coerceExact: 5,              // 数字字符串 -> 数字（语义无损，确定）
  coerceToString: 5,           // number/boolean -> string（确定）
  coerceBoolean: 10,           // 'true'/'false' -> boolean（需人工选择，语言相关）
  enumExact: 10,               // 跨表示精确匹配（需人工：类型表示已改变）
  enumNear: 10,                // 唯一最近枚举（确定）
  enumTie: 20,                 // 多个近似枚举并列（需人工，不任选）
  enumAny: 20,                 // 无近似：枚举全体列为人工候选
  removeField: 15,             // 删除触发 requires 的字段（确定，有破坏性）
  addEnumValue: 20,            // 缺失字段且声明了枚举：枚举值为人工候选
  pendingHuman: 100,           // 缺失字段且无枚举：必须人工提供（不依赖隐藏默认值）
  compareNumberFix: 5,         // compare 数字一侧确定调整
  compareEnumFix: 20,          // compare 枚举一侧（人工）
};

const ENUM_NEAR_MAX_DISTANCE = 2;

export function levenshtein(a: string, b: string): number {
  const m = a.length;
  const n = b.length;
  if (m === 0) return n;
  if (n === 0) return m;
  let prev = Array.from({ length: n + 1 }, (_, j) => j);
  for (let i = 1; i <= m; i++) {
    const cur = [i];
    for (let j = 1; j <= n; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      cur[j] = Math.min(cur[j - 1] + 1, prev[j] + 1, prev[j - 1] + cost);
    }
    prev = cur;
  }
  return prev[n];
}

interface PropInfo {
  schema: PropertySchema;
  rulePath: string; // 该属性 schema 的规则路径
  parentPath: string; // 实例上父对象路径
  parentRulePath: string;
  name: string;
}

// 在 schema 中按实例路径定位属性信息
export function locateProperty(schema: RootSchema, instancePtr: string): PropInfo | null {
  const segs = parsePtr(instancePtr);
  if (segs.length === 0) return null;
  let cur: PropertySchema = schema;
  let curRule = '';
  let curInst = '';
  for (let i = 0; i < segs.length; i++) {
    const name = segs[i];
    const props = cur.properties;
    if (!props || !(name in props)) return null;
    const nextRule = joinPtr(joinPtr(curRule, 'properties'), name);
    if (i === segs.length - 1) {
      return {
        schema: props[name],
        rulePath: nextRule,
        parentPath: curInst,
        parentRulePath: curRule,
        name,
      };
    }
    cur = props[name];
    curRule = nextRule;
    curInst = joinPtr(curInst, name);
  }
  return null;
}

function isPlainStringNumber(s: string): boolean {
  const t = s.trim();
  return t !== '' && Number.isFinite(Number(t));
}

// 前导零整数串（如 '001'）：转整数可能丢失编码语义 -> 需人工
function isLeadingZeroIntegerString(s: string): boolean {
  const t = s.trim();
  return /^[+-]?0\d+$/.test(t) && Number.isInteger(Number(t));
}

function isBooleanWord(s: string): boolean {
  return s === 'true' || s === 'false';
}

function basePatch(
  err: ValidationError,
  targetPath: string,
  instance: unknown,
  overrides: Omit<PatchCandidate, 'targetPath' | 'preconditionValue' | 'fixes'>,
): PatchCandidate {
  const cur = getAtPath(instance, targetPath);
  const preconditionValue = cur.exists
    ? { present: true as const, value: cur.value }
    : { present: false as const };
  return {
    targetPath,
    preconditionValue,
    fixes: [errorKey(err)],
    ...overrides,
  };
}

// ---- TYPE_MISMATCH 候选 ----
function typeMismatchCandidates(
  err: ValidationError,
  prop: PropInfo,
  instance: unknown,
): PatchCandidate[] {
  const current = getAtPath(instance, err.instancePath);
  const value = current.value;
  const target = prop.schema.type;
  const out: PatchCandidate[] = [];
  const mk = (ov: Omit<PatchCandidate, 'targetPath' | 'preconditionValue' | 'fixes'>) =>
    basePatch(err, err.instancePath, instance, ov);

  if (typeof value === 'string' && (target === 'integer' || target === 'number')) {
    if (isLeadingZeroIntegerString(value)) {
      // '001' -> 1 会丢失前导零编码语义，必须人工确认
      out.push(
        mk({
          op: 'set',
          value: Number(value.trim()),
          certainty: 'human',
          cost: COST.coerceBoolean,
          originRulePath: joinPtr(prop.rulePath, 'type'),
          reason: `字符串 ${JSON.stringify(value)} 含前导零，转为整数 ${Number(value.trim())} 可能丢失编码语义，需人工确认`,
        }),
      );
      return out;
    }
    if (isPlainStringNumber(value)) {
      const num = Number(value.trim());
      if (target === 'integer' && Number.isInteger(num)) {
        out.push(
          mk({
            op: 'set',
            value: num,
            certainty: 'certain',
            cost: COST.coerceExact,
            originRulePath: joinPtr(prop.rulePath, 'type'),
            reason: `数字字符串 ${JSON.stringify(value)} 无损转为整数 ${num}（确定）`,
          }),
        );
      } else if (target === 'number') {
        out.push(
          mk({
            op: 'set',
            value: num,
            certainty: 'certain',
            cost: COST.coerceExact,
            originRulePath: joinPtr(prop.rulePath, 'type'),
            reason: `数字字符串 ${JSON.stringify(value)} 无损转为数字 ${num}（确定）`,
          }),
        );
      } else if (target === 'integer' && !Number.isInteger(num)) {
        // 浮点 -> 整数：并列两种等代价方式，需人工选择，不任选
        out.push(
          mk({
            op: 'set',
            value: Math.trunc(num),
            certainty: 'human',
            cost: COST.coerceNumberFromNumber,
            originRulePath: joinPtr(prop.rulePath, 'type'),
            reason: `浮点字符串 ${JSON.stringify(value)} 截断为整数 ${Math.trunc(num)}（需人工选择取整方式）`,
          }),
          mk({
            op: 'set',
            value: Math.round(num),
            certainty: 'human',
            cost: COST.coerceNumberFromNumber,
            originRulePath: joinPtr(prop.rulePath, 'type'),
            reason: `浮点字符串 ${JSON.stringify(value)} 四舍五入为整数 ${Math.round(num)}（需人工选择取整方式）`,
          }),
        );
      }
      return out;
    }
    return out; // 非数字字符串 -> 数字/整数：无安全转换，不给候选
  }

  if (typeof value === 'number' && target === 'integer' && !Number.isInteger(value)) {
    out.push(
      mk({
        op: 'set',
        value: Math.trunc(value),
        certainty: 'human',
        cost: COST.coerceNumberFromNumber,
        originRulePath: joinPtr(prop.rulePath, 'type'),
        reason: `${value} 截断为整数 ${Math.trunc(value)}（需人工选择取整方式）`,
      }),
      mk({
        op: 'set',
        value: Math.round(value),
        certainty: 'human',
        cost: COST.coerceNumberFromNumber,
        originRulePath: joinPtr(prop.rulePath, 'type'),
        reason: `${value} 四舍五入为整数 ${Math.round(value)}（需人工选择取整方式）`,
      }),
    );
    return out;
  }

  if (typeof value === 'string' && isBooleanWord(value) && target === 'boolean') {
    out.push(
      mk({
        op: 'set',
        value: value === 'true',
        certainty: 'human',
        cost: COST.coerceBoolean,
        originRulePath: joinPtr(prop.rulePath, 'type'),
        reason: `字符串 ${JSON.stringify(value)} 解释为布尔 ${value === 'true'}（语言相关，需人工确认）`,
      }),
    );
    return out;
  }

  if ((typeof value === 'number' || typeof value === 'boolean') && target === 'string') {
    out.push(
      mk({
        op: 'set',
        value: String(value),
        certainty: 'certain',
        cost: COST.coerceToString,
        originRulePath: joinPtr(prop.rulePath, 'type'),
        reason: `${typeof value} 转为字符串 ${JSON.stringify(String(value))}（确定）`,
      }),
    );
    return out;
  }

  return out; // 其余组合无安全基本修复
}

// ---- ENUM_MISMATCH 候选 ----
function enumMismatchCandidates(
  err: ValidationError,
  prop: PropInfo,
  instance: unknown,
): PatchCandidate[] {
  const members = prop.schema.enum;
  const value = getAtPath(instance, err.instancePath).value;
  const out: PatchCandidate[] = [];
  const mk = (
    member: string | number,
    certainty: PatchCandidate['certainty'],
    cost: number,
    reason: string,
  ) =>
    basePatch(err, err.instancePath, instance, {
      op: 'set' as const,
      value: member,
      certainty,
      cost,
      originRulePath: joinPtr(prop.rulePath, 'enum'),
      reason,
    });

  // 1) 跨表示精确匹配（字符串 "1" 与数字 1）——改变类型表示，需人工
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    const sameText = members.filter((m) => String(m) === String(value) && m !== value);
    for (const m of sameText) {
      out.push(
        mk(m, 'human', COST.enumExact, `值 ${JSON.stringify(String(value))} 与枚举成员 ${JSON.stringify(m)} 文本相同但表示不同，需人工选择`),
      );
    }
  }

  // 2) 字符串枚举近似（编辑距离）：唯一最近 => 确定；并列 => 多个人工候选，不任选
  if (typeof value === 'string') {
    const strMembers = members.filter((m): m is string => typeof m === 'string');
    const scored = strMembers.map((m) => ({ m, d: levenshtein(value, m) }));
    if (scored.length > 0) {
      const minD = Math.min(...scored.map((s) => s.d));
      if (minD <= ENUM_NEAR_MAX_DISTANCE && minD > 0) {
        const winners = scored.filter((s) => s.d === minD);
        if (winners.length === 1) {
          out.push(
            mk(winners[0].m, 'certain', COST.enumNear, `唯一最近枚举（编辑距离 ${minD}）：${JSON.stringify(winners[0].m)}（确定）`),
          );
        } else {
          for (const w of winners) {
            out.push(
              mk(w.m, 'human', COST.enumTie, `多个近似枚举并列（编辑距离 ${minD}），不自动任选：${JSON.stringify(w.m)}`),
            );
          }
        }
      } else if (minD > ENUM_NEAR_MAX_DISTANCE) {
        // 无近似：不静默任选，全体成员列为人工候选
        for (const m of strMembers) {
          out.push(
            mk(m, 'human', COST.enumAny, `无近似枚举，请人工选择：${JSON.stringify(m)}`),
          );
        }
      }
    }
  }

  // 3) 非字符串/数字且无近似：全部枚举成员作为人工候选（去重后）
  if (out.length === 0) {
    for (const m of members) {
      out.push(mk(m, 'human', COST.enumAny, `请人工选择枚举成员：${JSON.stringify(m)}`));
    }
  }
  return out;
}

// ---- NULL_NOT_ALLOWED：null 无法安全自动替换，只能人工给值 ----
function nullCandidates(
  err: ValidationError,
  prop: PropInfo,
  instance: unknown,
): PatchCandidate[] {
  const out: PatchCandidate[] = [];
  if (prop.schema.enum) {
    for (const member of prop.schema.enum) {
      out.push(
        basePatch(err, err.instancePath, instance, {
          op: 'set',
          value: member,
          certainty: 'human',
          cost: COST.enumAny,
          originRulePath: joinPtr(prop.rulePath, 'enum'),
          reason: `当前为 null，请人工选择枚举成员：${JSON.stringify(member)}`,
        }),
      );
    }
    return out;
  }
  out.push(
    basePatch(err, err.instancePath, instance, {
      op: 'set',
      certainty: 'human',
      cost: COST.pendingHuman,
      originRulePath: joinPtr(prop.rulePath, 'nullable'),
      reason: `值为 null 且不允许为空，需人工提供新值（不使用任何隐藏默认值）`,
      pending: true,
    }),
  );
  return out;
}

// ---- 缺失字段补值（required / requires / compare 缺失侧共用） ----
function fillMissingCandidates(
  errors: ValidationError[],
  targetPath: string,
  prop: PropInfo,
  instance: unknown,
  originRulePath: string,
): PatchCandidate[] {
  const fixes = errors.map(errorKey);
  const cur = getAtPath(instance, targetPath);
  const precon = cur.exists
    ? { present: true as const, value: cur.value }
    : { present: false as const };
  const out: PatchCandidate[] = [];

  if (prop.schema.enum) {
    for (const member of prop.schema.enum) {
      out.push({
        targetPath,
        op: 'set',
        value: member,
        preconditionValue: precon,
        certainty: 'human',
        cost: COST.addEnumValue,
        originRulePath,
        reason: `补缺失字段 ${JSON.stringify(prop.name)}：枚举成员需人工选择 ${JSON.stringify(member)}`,
        fixes: [...fixes],
      });
    }
    return out;
  }

  // 无枚举可依：只能人工提供具体值，绝不注入隐藏默认值
  out.push({
    targetPath,
    op: 'set',
    preconditionValue: precon,
    certainty: 'human',
    cost: COST.pendingHuman,
    originRulePath,
    reason: `补缺失字段 ${JSON.stringify(prop.name)}：无枚举依据，需人工提供具体值（不依赖隐藏默认值）`,
    fixes: [...fixes],
    pending: true,
  });
  return out;
}

// ---- REQUIRES_MISSING：两条修复路线（补被要求字段 / 删除源字段） ----
function requiresCandidates(
  err: ValidationError,
  schema: RootSchema,
  instance: unknown,
): PatchCandidate[] {
  const out: PatchCandidate[] = [];
  const segs = parsePtr(err.rulePath); // ..., properties, <src>, requires
  const reqIdx = segs.lastIndexOf('requires');
  const srcName = segs[reqIdx - 1];
  const srcPath = joinPtr(parentPathOf(err.instancePath), srcName);
  const srcProp = locateProperty(schema, srcPath);

  // 路线 A：补被要求的缺失字段
  const targetProp = locateProperty(schema, err.instancePath);
  if (targetProp) {
    out.push(
      ...fillMissingCandidates(
        [err],
        err.instancePath,
        targetProp,
        instance,
        err.rulePath,
      ),
    );
  }

  // 路线 B：删除源字段（仅当它不是父对象必填字段，避免拆东墙补西墙）
  if (srcProp) {
    const parentRequired =
      (locateParentSchema(schema, srcPath)?.required ?? []);
    const sourceIsRequired = parentRequired.includes(srcName);
    if (!sourceIsRequired) {
      out.push(
        basePatch(err, srcPath, instance, {
          op: 'unset',
          certainty: 'certain',
          cost: COST.removeField,
          originRulePath: err.rulePath,
          reason: `删除触发跨字段约束的字段 ${JSON.stringify(srcName)}，从而不再要求其它字段（确定，但有破坏性）`,
        }),
      );
    }
  }
  return out;
}

function parentPathOf(ptr: string): string {
  const segs = parsePtr(ptr);
  return segs.slice(0, -1).map((s) => '/' + s.replace(/~/g, '~0').replace(/\//g, '~1')).join('');
}

function locateParentSchema(schema: RootSchema, childPtr: string): PropertySchema | null {
  const segs = parsePtr(childPtr);
  if (segs.length <= 1) return schema;
  let cur: PropertySchema = schema;
  for (let i = 0; i < segs.length - 1; i++) {
    if (!cur.properties || !(segs[i] in cur.properties)) return null;
    cur = cur.properties[segs[i]];
  }
  return cur;
}

// ---- COMPARE_FAILED ----
function compareCandidates(
  err: ValidationError,
  schema: RootSchema,
  instance: unknown,
): PatchCandidate[] {
  const out: PatchCandidate[] = [];
  if (!err.otherPath || !err.op) return out;
  const selfPath = err.instancePath;
  const otherPath = err.otherPath;
  const selfProp = locateProperty(schema, selfPath);
  const otherProp = locateProperty(schema, otherPath);

  // 缺失侧：按缺失字段处理（枚举人工选择，否则 pending）
  if (err.side === 'self' || err.side === 'both') {
    const cur = getAtPath(instance, selfPath);
    if (!cur.exists && selfProp) {
      out.push(...fillMissingCandidates([err], selfPath, selfProp, instance, err.rulePath));
    }
  }
  if (err.side === 'other' || err.side === 'both') {
    const cur = getAtPath(instance, otherPath);
    if (!cur.exists && otherProp) {
      out.push(...fillMissingCandidates([err], otherPath, otherProp, instance, err.rulePath));
    }
  }

  const selfVal = getAtPath(instance, selfPath).value;
  const otherVal = getAtPath(instance, otherPath).value;

  if (err.side === 'both' && typeof selfVal === 'number' && typeof otherVal === 'number') {
    const op = err.op;
    // 调整 self（确定的最小边界值）
    // self op other：反解 self 的最小可行值
    const selfTarget =
      op === '<' ? otherVal - 1 : op === '<=' ? otherVal : op === '>' ? otherVal + 1 : otherVal;
    if (selfProp?.schema.type === 'integer' && !Number.isInteger(selfTarget)) {
      // 由 self 侧枚举/整数边界决定；非整数边界不在此生成
    } else {
      out.push(
        basePatch(err, selfPath, instance, {
          op: 'set',
          value: selfTarget,
          certainty: 'certain',
          cost: COST.compareNumberFix,
          originRulePath: err.rulePath,
          reason: `调整 ${JSON.stringify(selfProp?.name ?? selfPath)} 为 ${selfTarget}，使 ${selfTarget} ${op} ${otherVal} 成立（确定）`,
        }),
      );
    }
    // 调整 other（确定的最小边界值）
    // 反解 other 的最小可行值：self < o => o=self+1；<= => o=self；> => o=self-1；>= => o=self
    const otherTarget =
      op === '<' ? selfVal + 1 : op === '<=' ? selfVal : op === '>' ? selfVal - 1 : selfVal;
    out.push(
      basePatch(err, otherPath, instance, {
        op: 'set',
        value: otherTarget,
        certainty: 'certain',
        cost: COST.compareNumberFix,
        originRulePath: err.rulePath,
        reason: `调整 ${JSON.stringify(otherProp?.name ?? otherPath)} 为 ${otherTarget}，使 ${selfVal} ${op} ${otherTarget} 成立（确定）`,
      }),
    );
  }

  if (
    err.side === 'both' &&
    typeof selfVal === 'string' &&
    typeof otherVal === 'string' &&
    selfProp?.schema.enum &&
    otherProp?.schema.enum &&
    JSON.stringify(selfProp.schema.enum) === JSON.stringify(otherProp.schema.enum)
  ) {
    const members = selfProp.schema.enum;
    const i = members.indexOf(selfVal);
    const j = members.indexOf(otherVal);
    if (i !== -1 && j !== -1) {
      for (let k = 0; k < members.length; k++) {
        if (k === i) continue;
        const okSelf = (() => {
          switch (err.op) {
            case '<': return k < j;
            case '<=': return k <= j;
            case '>': return k > j;
            case '>=': return k >= j;
            default: return false;
          }
        })();
        if (okSelf) {
          out.push(
            basePatch(err, selfPath, instance, {
              op: 'set',
              value: members[k],
              certainty: 'human',
              cost: COST.compareEnumFix,
              originRulePath: err.rulePath,
              reason: `调整枚举 ${JSON.stringify(selfProp.name)} 为 ${JSON.stringify(members[k])} 以满足顺序约束（人工选择）`,
            }),
          );
        }
      }
      for (let k = 0; k < members.length; k++) {
        if (k === j) continue;
        const okOther = (() => {
          switch (err.op) {
            case '<': return i < k;
            case '<=': return i <= k;
            case '>': return i > k;
            case '>=': return i >= k;
            default: return false;
          }
        })();
        if (okOther) {
          out.push(
            basePatch(err, otherPath, instance, {
              op: 'set',
              value: members[k],
              certainty: 'human',
              cost: COST.compareEnumFix,
              originRulePath: err.rulePath,
              reason: `调整枚举 ${JSON.stringify(otherProp.name)} 为 ${JSON.stringify(members[k])} 以满足顺序约束（人工选择）`,
            }),
          );
        }
      }
    }
  }

  return out;
}

// ---- 汇总为按目标路径互斥的候选组；同目标同值候选合并、累加 fixes ----
function patchIdentity(p: PatchCandidate): string {
  return JSON.stringify({
    op: p.op,
    value: p.value ?? null,
    pending: p.pending ?? false,
  });
}

function mergeInGroup(map: Map<string, PatchCandidate[]>, cand: PatchCandidate): void {
  let list = map.get(cand.targetPath);
  if (!list) {
    list = [];
    map.set(cand.targetPath, list);
  }
  const existing = list.find(
    (c) =>
      patchIdentity(c) === patchIdentity(cand) &&
      c.op === cand.op &&
      c.certainty === cand.certainty &&
      c.cost === cand.cost,
  );
  if (existing) {
    for (const f of cand.fixes) if (!existing.fixes.includes(f)) existing.fixes.push(f);
    return;
  }
  list.push(cand);
}

export function buildCandidateGroups(
  instance: unknown,
  schema: RootSchema,
  errors: ValidationError[],
): CandidateGroup[] {
  const map = new Map<string, PatchCandidate[]>();

  for (const err of errors) {
    const prop = locateProperty(schema, err.instancePath);

    switch (err.kind) {
      case 'TYPE_MISMATCH': {
        if (prop) {
          for (const c of typeMismatchCandidates(err, prop, instance)) mergeInGroup(map, c);
        }
        break;
      }
      case 'ENUM_MISMATCH': {
        if (prop && prop.schema.enum) {
          for (const c of enumMismatchCandidates(err, prop, instance)) mergeInGroup(map, c);
        }
        break;
      }
      case 'NULL_NOT_ALLOWED': {
        if (prop) for (const c of nullCandidates(err, prop, instance)) mergeInGroup(map, c);
        break;
      }
      case 'REQUIRED_MISSING': {
        if (prop) {
          for (const c of fillMissingCandidates(
            [err],
            err.instancePath,
            prop,
            instance,
            err.rulePath,
          )) {
            mergeInGroup(map, c);
          }
        }
        break;
      }
      case 'REQUIRES_MISSING': {
        for (const c of requiresCandidates(err, schema, instance)) mergeInGroup(map, c);
        break;
      }
      case 'COMPARE_FAILED': {
        for (const c of compareCandidates(err, schema, instance)) mergeInGroup(map, c);
        break;
      }
    }
  }

  const groups: CandidateGroup[] = [];
  for (const [targetPath, candidates] of [...map.entries()].sort((a, b) =>
    a[0] < b[0] ? -1 : 1,
  )) {
    groups.push({ targetPath, candidates });
  }
  return groups;
}
