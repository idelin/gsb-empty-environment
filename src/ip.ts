/**
 * 严格的 IP 字面量解析与公网判定。
 *
 * 不依赖 URL/Socket 的自动规范化，避免浏览器与 Node 对
 * 0x7f.1、0177.0.0.1、2130706433 等写法的解析差异影响安全判定。
 */

export type IpAddress = {
  kind: "ipv4" | "ipv6";
  bytes: number[];
};

const IPV4_NUMERIC_PART = /^(0[xX][0-9a-fA-F]+|0[0-7]*|[0-9]+)$/;

function parseIpv4Part(part: string): number | undefined {
  if (!IPV4_NUMERIC_PART.test(part)) {
    return undefined;
  }
  let value: number;
  if (part.startsWith("0x") || part.startsWith("0X")) {
    value = parseInt(part.slice(2), 16);
  } else if (part.length > 1 && part.startsWith("0")) {
    value = parseInt(part, 8);
  } else {
    value = parseInt(part, 10);
  }
  if (!Number.isSafeInteger(value) || value < 0) {
    return undefined;
  }
  return value;
}

/**
 * 解析宽松的 IPv4 字面量：
 * 支持点分十进制、十六进制（0x 前缀）、八进制（前导 0）以及
 * 1~4 段的整数压缩写法（如 2130706433、0x7f.1、0177.0.0.1）。
 */
export function parseIpv4(input: string): IpAddress | undefined {
  if (input.length === 0 || input.includes(" ")) {
    return undefined;
  }
  const rawParts = input.split(".");
  if (rawParts.length < 1 || rawParts.length > 4) {
    return undefined;
  }
  const numbers: number[] = [];
  for (const part of rawParts) {
    const value = parseIpv4Part(part);
    if (value === undefined) {
      return undefined;
    }
    numbers.push(value);
  }

  const octets: number[] = [];
  for (let index = 0; index < numbers.length; index += 1) {
    const isLast = index === numbers.length - 1;
    const width = isLast ? 5 - numbers.length : 1;
    const max = 0xffffffff >>> ((4 - width) * 8);
    if (numbers[index] > max) {
      return undefined;
    }
    if (isLast) {
      let rest = numbers[index];
      const tail: number[] = [];
      for (let byteIndex = 0; byteIndex < width; byteIndex += 1) {
        tail.push(rest & 0xff);
        rest = Math.floor(rest / 256);
      }
      octets.push(...tail.reverse());
    } else {
      octets.push(numbers[index] & 0xff);
    }
  }
  if (octets.length !== 4) {
    return undefined;
  }
  return { kind: "ipv4", bytes: octets };
}

const HEX_GROUP = /^[0-9a-fA-F]{1,4}$/;

/**
 * 解析 RFC 5952 风格的 IPv6 字面量，支持 :: 压缩与末尾内嵌 IPv4。
 */
export function parseIpv6(input: string): IpAddress | undefined {
  if (input.length === 0 || input.includes(" ")) {
    return undefined;
  }
  let address = input;
  const percentIndex = address.indexOf("%");
  if (percentIndex !== -1) {
    address = address.slice(0, percentIndex);
  }
  if (address.length === 0) {
    return undefined;
  }

  const doubleColonCount = (address.match(/::/g) ?? []).length;
  if (doubleColonCount > 1) {
    return undefined;
  }

  let headText: string;
  let tailText: string | undefined;
  const doubleColonIndex = address.indexOf("::");
  if (doubleColonIndex === -1) {
    headText = address;
  } else {
    headText = address.slice(0, doubleColonIndex);
    tailText = address.slice(doubleColonIndex + 2);
  }

  const headParts = headText.length > 0 ? headText.split(":") : [];
  const tailParts = tailText === undefined ? [] : tailText.split(":");
  const groups: number[] = [];

  const pushGroups = (parts: string[]): boolean => {
    for (const part of parts) {
      if (part === "") {
        return false;
      }
      if (part.includes(".")) {
        const embedded = parseIpv4(part);
        if (!embedded || embedded.kind !== "ipv4") {
          return false;
        }
        groups.push((embedded.bytes[0] << 8) | embedded.bytes[1]);
        groups.push((embedded.bytes[2] << 8) | embedded.bytes[3]);
      } else {
        if (!HEX_GROUP.test(part)) {
          return false;
        }
        groups.push(parseInt(part, 16));
      }
    }
    return true;
  };

  if (!pushGroups(headParts) || !pushGroups(tailParts)) {
    return undefined;
  }

  let sixteenBitGroups: number[];
  if (doubleColonIndex === -1) {
    if (groups.length !== 8) {
      return undefined;
    }
    sixteenBitGroups = groups;
  } else {
    if (groups.length > 7) {
      return undefined;
    }
    const zeroGroups = new Array(8 - groups.length).fill(0);
    const headGroups = headParts.length > 0 ? groups.slice(0, headParts.length) : [];
    const tailGroups = groups.slice(headGroups.length);
    sixteenBitGroups = [...headGroups, ...zeroGroups, ...tailGroups];
  }

  const bytes: number[] = [];
  for (const group of sixteenBitGroups) {
    bytes.push((group >> 8) & 0xff, group & 0xff);
  }
  return { kind: "ipv6", bytes };
}

/** 识别主机名是否为 IP 字面量（IPv6 需先去掉 URL 主机的方括号）。 */
export function parseIpLiteral(hostname: string): IpAddress | undefined {
  if (hostname.length === 0) {
    return undefined;
  }
  if (hostname.includes(":")) {
    return parseIpv6(hostname);
  }
  return parseIpv4(hostname);
}

function ipv4IsPublic(bytes: number[]): boolean {
  const [a, b] = bytes;
  if (a === 0) return false; // 0.0.0.0/8 “本网络”
  if (a === 10) return false; // RFC 1918
  if (a === 127) return false; // 环回
  if (a === 169 && b === 254) return false; // 链路本地（含云元数据）
  if (a === 172 && b >= 16 && b <= 31) return false; // RFC 1918
  if (a === 192 && b === 0 && bytes[2] === 0) return false; // IETF 协议分配
  if (a === 192 && b === 168) return false; // RFC 1918
  if (a === 100 && b >= 64 && b <= 127) return false; // CGNAT 100.64/10
  if (a >= 224) return false; // 组播 224/4 与保留 240/4
  if (a === 198 && (b === 18 || b === 19)) return false; // 基准测试
  if (a === 192 && b === 0 && bytes[2] === 2) return false; // TEST-NET-1
  if (a === 198 && b === 51 && bytes[2] === 100) return false; // TEST-NET-2
  if (a === 203 && b === 0 && bytes[2] === 113) return false; // TEST-NET-3
  return true;
}

function ipv6IsPublic(bytes: number[]): boolean {
  // ::/128 未指定、::1/128 环回
  if (bytes.slice(0, 15).every((byte) => byte === 0)) {
    return false;
  }
  // IPv4 映射/兼容地址，按内嵌 IPv4 判定
  const mappedPrefix = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255];
  if (bytes.slice(0, 12).every((byte, index) => byte === mappedPrefix[index])) {
    return ipv4IsPublic(bytes.slice(12));
  }
  if (bytes.slice(0, 12).every((byte) => byte === 0)) {
    return ipv4IsPublic(bytes.slice(12));
  }
  if (bytes[0] === 0xff) return false; // ff00::/8 组播
  if (bytes[0] === 0xfe && (bytes[1] & 0xc0) === 0x80) return false; // 链路本地
  if (bytes[0] === 0xfe && (bytes[1] & 0xc0) === 0xc0) return false; // 废弃站点本地
  if (bytes[0] === 0xfc || bytes[0] === 0xfd) return false; // fc00::/7 唯一本地
  if (bytes[0] === 0x20 && bytes[1] === 0x01 && bytes[2] === 0x0d && bytes[3] === 0xb8) {
    return false; // 2001:db8::/32 文档
  }
  if (bytes[0] === 0x20 && bytes[1] === 0x01 && bytes[2] === 0x00 && (bytes[3] & 0x30) === 0x20) {
    return false; // 2001:20::/28 ORCHID
  }
  if (bytes[0] === 0x20 && bytes[1] === 0x01 && bytes[2] === 0x00 && bytes[3] === 0x10) {
    return false; // 2001:10::/28 ORCHID v1
  }
  if (bytes[0] === 0x20 && bytes[1] === 0x01 && bytes[2] === 0x00 && bytes[3] === 0x00) {
    return false; // 2001::/32 Teredo 等特殊分配
  }
  if (bytes[0] === 0x20 && bytes[1] === 0x01 && bytes[2] === 0x02 && (bytes[3] & 0xf0) === 0) {
    return false; // 2001:2::/48 基准测试
  }
  // 仅 2000::/3 为可全球路由单播，其余一律非公网（默认拒绝）
  return (bytes[0] & 0xe0) === 0x20;
}

export function isPublicIp(address: IpAddress): boolean {
  return address.kind === "ipv4"
    ? ipv4IsPublic(address.bytes)
    : ipv6IsPublic(address.bytes);
}
