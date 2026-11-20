export interface Weight { readonly n:bigint; readonly d:bigint }
function gcd(a:bigint,b:bigint):bigint {while(b!==0n){const r=a%b;a=b;b=r;}return a;}
// Canonical v1 wire format. No approximation is permitted in wire keys.
export function readWeight(text:string):Weight {
  const m=/^(0|[1-9][0-9]*)\/([1-9][0-9]*)$/.exec(text);
  if(!m || m[0]!==text) throw new Error("invalid weight");
  const n=BigInt(m[1]),d=BigInt(m[2]);
  if(n>d || gcd(n,d)!==1n) throw new Error("noncanonical probability");
  return {n,d};
}
export function wireWeight(w:Weight):string {
  const value=w.n.toString()+"/"+w.d.toString();readWeight(value);return value;
}
