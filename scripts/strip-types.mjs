// Dev-only type erasure so tests can run where no tsc is available.
// The sources follow a restricted style that makes erasure safe:
// annotations appear only in interface/type blocks, function declaration
// headers, and `const/let name: Type = ...` initializers.

function removeDeclare(src) {
  const re = /\bdeclare\b/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    const start = m.index;
    let depth = 0;
    let i = start;
    for (; i < src.length; i++) {
      const c = src[i];
      if (c === "{") depth++;
      else if (c === "}") depth--;
      else if (c === ";" && depth === 0) {
        i++;
        break;
      }
    }
    if (src[i] === "\n") i++;
    src = src.slice(0, start) + src.slice(i);
    re.lastIndex = 0;
  }
  return src;
}

function removeInterfaces(src) {
  const re = /\binterface\s+[A-Za-z_$][\w$]*[^{]*\{/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    let start = m.index;
    const lineStart = src.lastIndexOf("\n", start) + 1;
    const before = src.slice(lineStart, start);
    if (/^\s*export\s*$/.test(before)) start = lineStart + before.indexOf("export");
    let depth = 0;
    let i = m.index + m[0].length - 1;
    for (; i < src.length; i++) {
      if (src[i] === "{") depth++;
      else if (src[i] === "}") {
        depth--;
        if (depth === 0) {
          i++;
          break;
        }
      }
    }
    if (src[i] === "\n") i++;
    src = src.slice(0, start) + src.slice(i);
    re.lastIndex = 0;
  }
  return src;
}

export function stripTypes(source) {
  let src = source;
  src = removeDeclare(src);
  src = src.replace(/^[ \t]*import\s+type\s[^;]*;[ \t]*\r?\n?/gm, "");
  src = removeInterfaces(src);
  src = src.replace(/^[ \t]*(?:export\s+)?type\s+[A-Za-z_$][\w$]*\s*=[^;]*;[ \t]*\r?\n?/gm, "");
  src = src.replace(
    /\bfunction(\s+[A-Za-z_$][\w$]*)?\s*\(([^()]*)\)\s*(?::\s*([^\{]+?))?\s*\{/g,
    (m, name, params) => {
      const cleanParams = params.replace(/:\s*[^,)=]+/g, "");
      return "function" + (name || "") + "(" + cleanParams + ") {";
    }
  );
  src = src.replace(/\b(const|let)\s+([A-Za-z_$][\w$]*)\s*:\s*([^=;]+?)\s*=/g, "$1 $2 =");
  src = src.replace(/(from\s*["'])(\.[^"']*?)(["'])/g, (m, pre, spec, post) => {
    const withExt = /\.(mjs|js|ts|json)$/.test(spec) ? spec.replace(/\.ts$/, ".mjs") : spec + ".mjs";
    return pre + withExt + post;
  });
  return src;
}
