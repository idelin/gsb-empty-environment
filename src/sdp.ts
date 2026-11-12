export interface MediaSection {
  media: string;
  port: string;
  proto: string;
  formats: string[];
  attributes: Map<string, string[]>;
}

export interface Sdp {
  sessionAttributes: Map<string, string[]>;
  media: MediaSection[];
}

function addAttribute(map: Map<string, string[]>, line: string): void {
  const body = line.slice(2);
  const colon = body.indexOf(":");
  const name = colon === -1 ? body : body.slice(0, colon);
  const value = colon === -1 ? "" : body.slice(colon + 1);
  const list = map.get(name);
  if (list) {
    list.push(value);
  } else {
    map.set(name, [value]);
  }
}

export function parseSdp(sdp: string): Sdp {
  const sessionAttributes = new Map<string, string[]>();
  const media: MediaSection[] = [];
  let current: MediaSection | null = null;

  for (const rawLine of sdp.split(/\r\n|\r|\n/)) {
    const line = rawLine.trim();
    if (line.length < 2 || line[1] !== "=") continue;

    if (line.startsWith("m=")) {
      const parts = line.slice(2).split(/\s+/);
      current = {
        media: parts[0] ?? "",
        port: parts[1] ?? "",
        proto: parts[2] ?? "",
        formats: parts.slice(3),
        attributes: new Map(),
      };
      media.push(current);
    } else if (line.startsWith("a=")) {
      addAttribute(current ? current.attributes : sessionAttributes, line);
    }
  }

  return { sessionAttributes, media };
}

export function effectiveAttribute(
  sdp: Sdp,
  mediaIndex: number,
  name: string,
): string[] {
  const section = sdp.media[mediaIndex];
  const local = section?.attributes.get(name);
  if (local && local.length > 0) return local;
  return sdp.sessionAttributes.get(name) ?? [];
}

export interface Candidate {
  foundation: string;
  component: string;
  transport: string;
  priority: string;
  address: string;
  port: string;
  type: string;
  raw: string;
}

export function parseCandidate(value: string): Candidate | null {
  const body = value.startsWith("candidate:") ? value.slice("candidate:".length) : value;
  const parts = body.split(/\s+/);
  if (parts.length < 8) return null;
  let type = "";
  for (let i = 6; i + 1 < parts.length; i += 2) {
    if (parts[i] === "typ") {
      type = parts[i + 1];
      break;
    }
  }
  return {
    foundation: parts[0],
    component: parts[1],
    transport: parts[2],
    priority: parts[3],
    address: parts[4],
    port: parts[5],
    type,
    raw: value,
  };
}
