import {
  AddedStatement,
  CoverageSource,
  InheritedCoverage,
  MappingRule,
  MigrationReport,
  NewCoverageModel,
  NewStatement,
  OldCoverageData,
  OldStatement,
  UncertainItem,
} from "./types";
import { normalizeStatementText, rangeKey } from "./ranges";
import { ruleLabel, ruleNewIds, ruleOldIds } from "./rules";

interface ProvenLink {
  oldIds: string[];
  newIds: string[];
  provenance: string;
}

interface KeyedId {
  id: string;
  key: string;
}

interface MatchContext {
  oldById: Map<string, OldStatement>;
  newById: Map<string, NewStatement>;
  links: ProvenLink[];
  matchedOld: Set<string>;
  matchedNew: Set<string>;
  blockedOld: Set<string>;
  blockedNew: Set<string>;
  uncertainReasons: Map<string, string>;
  uncertainCandidates: Map<string, string[]>;
  diagnostics: string[];
}

function textOf(statement: OldStatement): string {
  return statement.text !== undefined ? statement.text : "";
}

function textOfNew(statement: NewStatement): string {
  return statement.text !== undefined ? statement.text : "";
}

function unmatchedOldStatements(ctx: MatchContext, oldData: OldCoverageData): OldStatement[] {
  return oldData.statements.filter(function (s) {
    return !ctx.matchedOld.has(s.id) && !ctx.blockedOld.has(s.id);
  });
}

function unmatchedNewStatements(ctx: MatchContext, newData: NewCoverageModel): NewStatement[] {
  return newData.statements.filter(function (s) {
    return !ctx.matchedNew.has(s.id) && !ctx.blockedNew.has(s.id);
  });
}

function applyExplicitRules(ctx: MatchContext, rules: MappingRule[]): void {
  const useCountOld = new Map<string, number>();
  const useCountNew = new Map<string, number>();
  for (const rule of rules) {
    for (const id of ruleOldIds(rule)) {
      useCountOld.set(id, (useCountOld.get(id) || 0) + 1);
    }
    for (const id of ruleNewIds(rule)) {
      useCountNew.set(id, (useCountNew.get(id) || 0) + 1);
    }
  }
  for (const entry of useCountOld) {
    if (entry[1] > 1) {
      ctx.blockedOld.add(entry[0]);
      ctx.uncertainReasons.set(
        entry[0],
        "referenced by " + entry[1] + " conflicting explicit rules"
      );
      ctx.diagnostics.push(
        "conflict: old statement " + entry[0] + " is referenced by " + entry[1] + " explicit rules"
      );
    }
  }
  for (const entry of useCountNew) {
    if (entry[1] > 1) {
      ctx.blockedNew.add(entry[0]);
      ctx.diagnostics.push(
        "conflict: new statement " + entry[0] + " is referenced by " + entry[1] + " explicit rules"
      );
    }
  }
  for (const rule of rules) {
    const oldIds = ruleOldIds(rule);
    const newIds = ruleNewIds(rule);
    const label = ruleLabel(rule);
    let valid = true;
    for (const id of oldIds) {
      if (!ctx.oldById.has(id)) {
        ctx.diagnostics.push("rule " + label + " references unknown old statement " + id);
        valid = false;
      }
    }
    for (const id of newIds) {
      if (!ctx.newById.has(id)) {
        ctx.diagnostics.push("rule " + label + " references unknown new statement " + id);
        valid = false;
      }
    }
    if (!valid) {
      continue;
    }
    const touchesConflict =
      oldIds.some(function (id) {
        return ctx.blockedOld.has(id);
      }) ||
      newIds.some(function (id) {
        return ctx.blockedNew.has(id);
      });
    if (touchesConflict) {
      continue;
    }
    ctx.links.push({ oldIds: oldIds, newIds: newIds, provenance: label });
    for (const id of oldIds) {
      ctx.matchedOld.add(id);
    }
    for (const id of newIds) {
      ctx.matchedNew.add(id);
    }
  }
}

function autoMatch(
  ctx: MatchContext,
  provenance: string,
  oldEntries: KeyedId[],
  newEntries: KeyedId[]
): void {
  const newByKey = new Map<string, string[]>();
  for (const entry of newEntries) {
    const list = newByKey.get(entry.key);
    if (list) {
      list.push(entry.id);
    } else {
      newByKey.set(entry.key, [entry.id]);
    }
  }
  const oldByKey = new Map<string, string[]>();
  for (const entry of oldEntries) {
    const list = oldByKey.get(entry.key);
    if (list) {
      list.push(entry.id);
    } else {
      oldByKey.set(entry.key, [entry.id]);
    }
  }
  for (const entry of oldByKey) {
    const key = entry[0];
    const oldIds = entry[1];
    const newIds = newByKey.get(key);
    if (!newIds) {
      continue;
    }
    if (oldIds.length === 1 && newIds.length === 1) {
      ctx.links.push({ oldIds: [oldIds[0]], newIds: [newIds[0]], provenance: provenance });
      ctx.matchedOld.add(oldIds[0]);
      ctx.matchedNew.add(newIds[0]);
    } else {
      for (const oldId of oldIds) {
        ctx.uncertainReasons.set(
          oldId,
          "ambiguous " +
            provenance +
            " match: " +
            oldIds.length +
            " old and " +
            newIds.length +
            " new statement(s) share the same key"
        );
        ctx.uncertainCandidates.set(oldId, newIds.slice());
      }
      ctx.diagnostics.push(
        "ambiguous " +
          provenance +
          ": " +
          oldIds.length +
          " old and " +
          newIds.length +
          " new statements share key " +
          key
      );
    }
  }
}

export function migrateCoverage(
  oldData: OldCoverageData,
  newData: NewCoverageModel,
  rules: MappingRule[]
): MigrationReport {
  const diagnostics: string[] = [];
  const oldById = new Map<string, OldStatement>();
  for (const stmt of oldData.statements) {
    if (oldById.has(stmt.id)) {
      diagnostics.push("duplicate old statement id: " + stmt.id);
    }
    oldById.set(stmt.id, stmt);
  }
  const newById = new Map<string, NewStatement>();
  for (const stmt of newData.statements) {
    if (newById.has(stmt.id)) {
      diagnostics.push("duplicate new statement id: " + stmt.id);
    }
    newById.set(stmt.id, stmt);
  }

  const ctx: MatchContext = {
    oldById: oldById,
    newById: newById,
    links: [],
    matchedOld: new Set<string>(),
    matchedNew: new Set<string>(),
    blockedOld: new Set<string>(),
    blockedNew: new Set<string>(),
    uncertainReasons: new Map<string, string>(),
    uncertainCandidates: new Map<string, string[]>(),
    diagnostics: diagnostics,
  };

  applyExplicitRules(ctx, rules);

  autoMatch(
    ctx,
    "auto:identical-range",
    unmatchedOldStatements(ctx, oldData).map(function (s) {
      return { id: s.id, key: rangeKey(s.file, s.range) };
    }),
    unmatchedNewStatements(ctx, newData).map(function (s) {
      return { id: s.id, key: rangeKey(s.file, s.range) };
    })
  );

  autoMatch(
    ctx,
    "auto:content-hash",
    unmatchedOldStatements(ctx, oldData)
      .filter(function (s) {
        return textOf(s).length > 0;
      })
      .map(function (s) {
        return { id: s.id, key: normalizeStatementText(textOf(s)) };
      }),
    unmatchedNewStatements(ctx, newData)
      .filter(function (s) {
        return textOfNew(s).length > 0;
      })
      .map(function (s) {
        return { id: s.id, key: normalizeStatementText(textOfNew(s)) };
      })
  );

  const inherited: InheritedCoverage[] = [];
  for (const stmt of newData.statements) {
    if (!ctx.matchedNew.has(stmt.id)) {
      continue;
    }
    const sources: CoverageSource[] = [];
    const provenance: string[] = [];
    for (const link of ctx.links) {
      if (link.newIds.indexOf(stmt.id) < 0) {
        continue;
      }
      if (provenance.indexOf(link.provenance) < 0) {
        provenance.push(link.provenance);
      }
      for (const oldId of link.oldIds) {
        const already = sources.some(function (s) {
          return s.oldId === oldId;
        });
        if (already) {
          continue;
        }
        const oldStmt = ctx.oldById.get(oldId);
        if (!oldStmt) {
          continue;
        }
        sources.push({
          oldId: oldStmt.id,
          file: oldStmt.file,
          range: oldStmt.range,
          hits: oldStmt.hits,
        });
      }
    }
    inherited.push({
      newId: stmt.id,
      file: stmt.file,
      range: stmt.range,
      provenance: provenance,
      sources: sources,
    });
  }

  const added: AddedStatement[] = [];
  for (const stmt of newData.statements) {
    if (ctx.matchedNew.has(stmt.id)) {
      continue;
    }
    added.push({ newId: stmt.id, file: stmt.file, range: stmt.range });
  }

  const uncertain: UncertainItem[] = [];
  for (const stmt of oldData.statements) {
    if (ctx.matchedOld.has(stmt.id)) {
      continue;
    }
    const reason =
      ctx.uncertainReasons.get(stmt.id) || "no proven counterpart in new version";
    const candidates = ctx.uncertainCandidates.get(stmt.id) || [];
    uncertain.push({
      oldId: stmt.id,
      file: stmt.file,
      range: stmt.range,
      hits: stmt.hits,
      reason: reason,
      candidates: candidates,
    });
  }

  return {
    inherited: inherited,
    added: added,
    uncertain: uncertain,
    diagnostics: diagnostics,
    summary: {
      oldTotal: oldData.statements.length,
      newTotal: newData.statements.length,
      inherited: inherited.length,
      added: added.length,
      uncertain: uncertain.length,
    },
  };
}
