const REGEX_SPECIALS = /[.+^${}()|[\]\\]/g;

/**
 * Compile a path glob into a RegExp.
 * Supported syntax:
 *   `**`  matches any number of characters including `/` (any depth)
 *   `*`   matches any characters except `/` (single path segment)
 *   `?`   matches exactly one character except `/`
 * Matching is anchored to the whole path; a leading `/` is ignored.
 */
export function compileGlob(pattern: string): RegExp {
  const normalized = pattern.replace(/^\/+/, "");
  let source = "";
  let i = 0;
  while (i < normalized.length) {
    const ch = normalized[i];
    if (ch === "*") {
      if (normalized[i + 1] === "*") {
        // `**/` also matches zero directories so `a/**/b` covers `a/b`.
        if (normalized[i + 2] === "/") {
          source += "(?:.*/)?";
          i += 3;
        } else {
          source += ".*";
          i += 2;
        }
      } else {
        source += "[^/]*";
        i += 1;
      }
    } else if (ch === "?") {
      source += "[^/]";
      i += 1;
    } else {
      source += ch.replace(REGEX_SPECIALS, "\\$&");
      i += 1;
    }
  }
  return new RegExp(`^${source}$`);
}

export function matchGlob(pattern: string, path: string): boolean {
  return compileGlob(pattern).test(path.replace(/^\/+/, ""));
}
