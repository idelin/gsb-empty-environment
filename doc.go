// Package netsec provides admission control for containerd certs.d
// hosts.toml configurations before they are written to disk, plus a
// digest-verified pull loop that fails over between hosts.
//
// The typical configuration-service flow is:
//
//   - parse a hosts.toml file into a Config;
//   - call Validate with the caller's Policy before persisting it;
//   - at pull time call HostsForPull to obtain the hosts allowed to
//     serve pulls (resolve-only mirrors are excluded) and PullByDigest
//     to fetch digest-referenced content with failover.
//
// Security rules enforced by Validate:
//
//   - plaintext http endpoints and skip_verify=true are rejected unless
//     the endpoint is loopback and Policy explicitly allows the
//     loopback exception;
//   - an Authorization header is only accepted on a host whose
//     normalized host identity (case-insensitive, trailing-dot and
//     default-port aware) matches the server's;
//   - endpoint URLs must be absolute http(s) URLs without userinfo;
//   - client entries must set both cert and key, and capabilities must
//     be among resolve, pull and push.
//
// PullByDigest additionally guarantees that content whose digest does
// not match the requested digest is discarded via Content.Discard
// before the next host is contacted, so tampered bytes are never
// committed or reused.
package netsec
