'use strict';

const { hash } = require('./hash');

class CompileError extends Error {
  constructor(moduleId, message) {
    super(`${moduleId}: ${message}`);
    this.name = 'CompileError';
    this.moduleId = moduleId;
  }
}

function normalizeType(text) {
  return text.replace(/\s+/g, ' ').trim();
}

/** Infer a shallow type for an unannotated const initializer. */
function inferInitializerType(init) {
  const t = init.trim();
  if (/^[-+]?\d/.test(t)) return 'number';
  if (/^['"`]/.test(t)) return 'string';
  if (/^(true|false)\b/.test(t)) return 'boolean';
  return 'unknown';
}

/**
 * Extract the export signature of a module: a map of exported name ->
 * normalized declaration text WITHOUT implementation bodies.
 * Only changes to this signature may invalidate dependents.
 */
function extractExports(source) {
  const exports = new Map();
  const add = (name, kind, typeText) => {
    exports.set(name, `${kind} ${name}: ${normalizeType(typeText)}`);
  };

  // export interface / type (pure types: full text is the signature)
  const interfaceRe = /export\s+interface\s+([A-Za-z_$][\w$]*)[^{]*\{([\s\S]*?)\}/g;
  let m;
  while ((m = interfaceRe.exec(source)) !== null) {
    add(m[1], 'interface', `{${m[2]}}`);
  }
  const typeRe = /export\s+type\s+([A-Za-z_$][\w$]*)\s*=\s*([^;]+);/g;
  while ((m = typeRe.exec(source)) !== null) {
    add(m[1], 'type', `= ${m[2]}`);
  }

  // export function name(params): ret {
  const fnRe = /export\s+(async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(([^)]*)\)\s*(?::\s*([^\{;]+))?/g;
  while ((m = fnRe.exec(source)) !== null) {
    const prefix = m[1] ? 'async function' : 'function';
    add(m[2], prefix, `(${m[3]}): ${(m[4] || 'void').trim()}`);
  }

  // export const/let/var name[: Type] = init
  const constRe = /export\s+(const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::\s*([^=;]+))?(?:=\s*([^;]+))?;/g;
  while ((m = constRe.exec(source)) !== null) {
    const type = m[3] ? m[3].trim() : (m[4] ? inferInitializerType(m[4]) : 'unknown');
    add(m[2], m[1], type);
  }

  // export class Name ... { ... }  -> keep member signatures, drop bodies
  const classRe = /export\s+(abstract\s+)?class\s+([A-Za-z_$][\w$]*)[^{]*\{/g;
  while ((m = classRe.exec(source)) !== null) {
    const bodyStart = classRe.lastIndex;
    const members = extractClassMembers(source, bodyStart);
    add(m[2], 'class', `{ ${members.join('; ')} }`);
  }

  // export { a, b as c }
  const namedRe = /export\s*\{([^}]+)\}/g;
  while ((m = namedRe.exec(source)) !== null) {
    for (const part of m[1].split(',')) {
      const asMatch = part.trim().match(/(?:[\w$]+\s+as\s+)?([A-Za-z_$][\w$]*)$/);
      if (asMatch) add(asMatch[1], 'binding', 're-exported');
    }
  }

  // export default <function|class|expr>
  const defaultFn = /export\s+default\s+function\s*\(([^)]*)\)\s*(?::\s*([^\{;]+))?/.exec(source);
  if (defaultFn) {
    add('default', 'function', `(${defaultFn[1]}): ${(defaultFn[2] || 'void').trim()}`);
  } else if (/export\s+default\s+class\b/.test(source)) {
    add('default', 'class', 'default class');
  } else if (/export\s+default\s+/.test(source)) {
    add('default', 'binding', 'default export');
  }

  return exports;
}

/** Scan a class body starting after its opening brace; return member signatures at depth 1. */
function extractClassMembers(source, bodyStart) {
  const members = [];
  let depth = 1;
  let current = '';
  for (let i = bodyStart; i < source.length && depth > 0; i++) {
    const ch = source[i];
    if (ch === '{') {
      if (depth === 1) {
        // method body begins: signature is what we accumulated
        const sig = current.trim();
        if (sig) members.push(sig);
        current = '';
      }
      depth++;
    } else if (ch === '}') {
      depth--;
    } else if (depth === 1) {
      if (ch === ';' || ch === '\n') {
        const sig = current.trim().replace(/=[\s\S]*$/, '').trim(); // drop field initializer
        if (sig) members.push(sig);
        current = '';
      } else {
        current += ch;
      }
    }
  }
  return members;
}

/** Serialize a signature map deterministically. */
function serializeSignature(exports) {
  return [...exports.entries()].sort(([a], [b]) => (a < b ? -1 : 1)).map(([, v]) => v).join('\n');
}

function signatureHash(exports) {
  return hash(serializeSignature(exports));
}

function checkBalanced(moduleId, source) {
  const pairs = { ')': '(', ']': '[', '}': '{' };
  const stack = [];
  for (const ch of source) {
    if (ch === '(' || ch === '[' || ch === '{') stack.push(ch);
    else if (pairs[ch]) {
      if (stack.pop() !== pairs[ch]) {
        throw new CompileError(moduleId, `unbalanced delimiter near '${ch}'`);
      }
    }
  }
  if (stack.length > 0) {
    throw new CompileError(moduleId, `unclosed '${stack[stack.length - 1]}'`);
  }
}

/**
 * "Compile" a TypeScript module: strip type annotations and type-only
 * declarations, producing JavaScript output. Throws CompileError on
 * malformed input; failed compilations produce no artifact.
 */
function compile(moduleId, source) {
  checkBalanced(moduleId, source);
  let js = source;
  js = js.replace(/export\s+interface\s+[A-Za-z_$][\w$]*[^{]*\{[\s\S]*?\}/g, '');
  js = js.replace(/export\s+type\s+[A-Za-z_$][\w$]*\s*=\s*[^;]+;/g, '');
  js = js.replace(/import\s+type\s+[^;]+;/g, '');
  // parameter / variable / return type annotations
  js = js.replace(/:\s*[A-Za-z_$][\w$]*(?:<[^>]*>)?(?:\[\])?(?=\s*[,)=;{])/g, '');
  return { js, signature: extractExports(source) };
}

module.exports = { compile, extractExports, serializeSignature, signatureHash, CompileError };
