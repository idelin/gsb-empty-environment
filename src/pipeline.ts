import { decideLinkRel, decideWindowOpen } from "./noopener.ts";

export interface PipelineOptions {
  keepOpener?: boolean;
}

function readAttr(tag: string, name: string): string | undefined {
  const re = new RegExp(`\\b${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^\\s>]+))`, "i");
  const match = re.exec(tag);
  if (!match) return undefined;
  return match[1] ?? match[2] ?? match[3];
}

function replaceAttr(tag: string, name: string, value: string): string {
  const re = new RegExp(`(\\b${name}\\s*=\\s*)("[^"]*"|'[^']*'|[^\\s>]+)`, "i");
  return tag.replace(re, `$1"${value}"`);
}

function insertAttr(tag: string, attr: string): string {
  const selfClosing = /\/\s*>$/.test(tag);
  const head = tag.replace(/\s*\/?\s*>$/, "");
  return `${head} ${attr}${selfClosing ? " />" : ">"}`;
}

export function rewriteAnchorTags(html: string, options: PipelineOptions = {}): string {
  return html.replace(/<a\b[^>]*>/gi, (tag) => {
    const target = readAttr(tag, "target");
    if (target === undefined) return tag;
    const rel = readAttr(tag, "rel");
    const decision = decideLinkRel({ target, rel });
    if (!decision.changed) return tag;
    if (rel === undefined) return insertAttr(tag, `rel="${decision.value}"`);
    return replaceAttr(tag, "rel", decision.value);
  });
}

interface ParsedArgs {
  args: string[];
  end: number;
}

function unescapeString(raw: string): string {
  return raw.replace(/\\(.)/g, (_all, ch: string) => {
    switch (ch) {
      case "n":
        return "\n";
      case "t":
        return "\t";
      case "r":
        return "\r";
      default:
        return ch;
    }
  });
}

function parseStringArgs(source: string, start: number): ParsedArgs | null {
  const args: string[] = [];
  let index = start;
  const skipWs = () => {
    while (index < source.length && /\s/.test(source[index])) index += 1;
  };
  skipWs();
  if (source[index] === ")") return { args, end: index + 1 };
  for (;;) {
    skipWs();
    const quote = source[index];
    if (quote !== "'" && quote !== '"') return null;
    let raw = "";
    index += 1;
    while (index < source.length && source[index] !== quote) {
      if (source[index] === "\\" && index + 1 < source.length) {
        raw += source.slice(index, index + 2);
        index += 2;
      } else {
        raw += source[index];
        index += 1;
      }
    }
    if (index >= source.length) return null;
    index += 1;
    args.push(unescapeString(raw));
    skipWs();
    if (source[index] === ",") {
      index += 1;
      continue;
    }
    if (source[index] === ")") return { args, end: index + 1 };
    return null;
  }
}

export function rewriteWindowOpenCalls(source: string, options: PipelineOptions = {}): string {
  const marker = /window\.open\(/g;
  let output = "";
  let last = 0;
  for (let match = marker.exec(source); match; match = marker.exec(source)) {
    const argsStart = match.index + match[0].length;
    const parsed = parseStringArgs(source, argsStart);
    if (!parsed) continue;
    const [url = "", target = "", features = ""] = parsed.args;
    const decision = decideWindowOpen({
      target: parsed.args.length > 1 ? target : undefined,
      features: parsed.args.length > 2 ? features : undefined,
      keepOpener: options.keepOpener,
    });
    output += source.slice(last, match.index);
    last = parsed.end;
    if (!decision.changed) {
      output += source.slice(match.index, parsed.end);
      continue;
    }
    const rebuilt = [url, decision.target, decision.value];
    output += `window.open(${rebuilt.map((arg) => JSON.stringify(arg)).join(", ")})`;
  }
  output += source.slice(last);
  return output;
}

export function rewriteDocument(html: string, options: PipelineOptions = {}): string {
  return rewriteWindowOpenCalls(rewriteAnchorTags(html, options), options);
}
