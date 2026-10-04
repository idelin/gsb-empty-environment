// Minimal type-stripper for the controlled TypeScript subset used in
// src/seam-carver.ts. The real project should be compiled with `tsc`;
// this exists only so the logic can be executed and tested on a plain
// Node runtime where the TypeScript compiler is unavailable.

const countChar = (s, ch) => s.split(ch).length - 1;

export function stripTypes(src) {
  const lines = src.split("\n");
  const kept = [];
  let depth = 0;
  let skipping = false;
  for (const line of lines) {
    if (skipping) {
      depth += countChar(line, "{") - countChar(line, "}");
      if (depth <= 0) skipping = false;
      continue;
    }
    if (/^\s*(export\s+)?interface\s+[A-Za-z_$]/.test(line)) {
      depth = countChar(line, "{") - countChar(line, "}");
      if (depth > 0) skipping = true;
      continue;
    }
    if (/^\s*(export\s+)?type\s+[A-Za-z_$][\w$]*\s*=/.test(line)) continue;
    kept.push(line);
  }
  let js = kept.join("\n");
  js = js.replace(/\b(private|public|readonly)\s+/g, "");
  js = js.replace(/(\w)\?:(?=\s)/g, "$1:");
  js = js.replace(
    /([A-Za-z_$\)\]]):\s*[A-Za-z_$][\w$<>\[\]|]*(?:\s*\|\s*[A-Za-z_$][\w$<>\[\]|]*)*(?=\s*[,)=;{]|\s*$)/gm,
    "$1"
  );
  return js;
}
