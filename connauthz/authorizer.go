package connauthz

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"sync/atomic"
)

var (
	// ErrNoClientCertificate 表示对端没有出示客户端证书。
	ErrNoClientCertificate = errors.New("connauthz: no client certificate presented")
	// ErrMalformedChain 表示记录到的证书链无法解析。
	ErrMalformedChain = errors.New("connauthz: malformed certificate chain")
	// ErrUntrustedChain 表示证书链在当前策略下无法构建到信任根的路径。
	ErrUntrustedChain = errors.New("connauthz: certificate chain is not trusted")
	// ErrRevoked 表示候选路径上存在本代已撤销的证书。
	ErrRevoked = errors.New("connauthz: certificate has been revoked")
)

// DenialError 描述一次开流被拒绝的原因及其判定所依据的策略代次。
type DenialError struct {
	Generation uint64
	Reason     error
	Detail     string
}

func (e *DenialError) Error() string {
	if e.Detail == "" {
		return e.Reason.Error() + " (generation " + uitoa(e.Generation) + ")"
	}
	return e.Reason.Error() + " (generation " + uitoa(e.Generation) + "): " + e.Detail
}

// Unwrap 暴露内部原因，支持 errors.Is。
func (e *DenialError) Unwrap() error { return e.Reason }

// Decision 是一次开流授权的结论。
type Decision struct {
	Allowed    bool
	Generation uint64
	// Peer 为对端叶子证书。
	Peer *x509.Certificate
	// ChosenChain 为命中的可信路径（叶子在前、根在后，含根）。
	ChosenChain []*x509.Certificate
	// VerifiedChains 为 x509 构建出的全部可信路径，便于观测交叉签名路径。
	VerifiedChains [][]*x509.Certificate

	reason error
	detail string
}

// Err 将拒绝原因包装为 *DenialError；允许时返回 nil。
func (d *Decision) Err() error {
	if d.Allowed {
		return nil
	}
	return &DenialError{Generation: d.Generation, Reason: d.reason, Detail: d.detail}
}

// Authorizer 持有当前最新一代策略，并对开流请求做复验。
// 零值不可用，请用 NewAuthorizer 构造。
type Authorizer struct {
	current atomic.Pointer[Policy]
}

// NewAuthorizer 以第一代策略创建授权器。
func NewAuthorizer(initial *Policy) (*Authorizer, error) {
	if initial == nil {
		return nil, errors.New("connauthz: initial policy is nil")
	}
	a := &Authorizer{}
	a.current.Store(initial)
	return a, nil
}

// Current 返回当前生效策略。
func (a *Authorizer) Current() *Policy { return a.current.Load() }

// Rotate 原子换入新一代策略。新一代代次必须严格大于当前代次，
// 否则拒绝换入并返回错误、保持现状，从而保证代次永不回退。
//
// Rotate 成功返回后，后续所有 Authorize 调用都观察到新策略；
// 与 Rotate 并发的调用要么完整使用旧代、要么完整使用新代。
func (a *Authorizer) Rotate(next *Policy) error {
	if next == nil {
		return errors.New("connauthz: next policy is nil")
	}
	for {
		cur := a.current.Load()
		if next.generation <= cur.generation {
			return &DenialError{
				Generation: cur.generation,
				Reason:     errors.New("connauthz: stale policy generation"),
				Detail:     "got " + uitoa(next.generation) + ", current " + uitoa(cur.generation),
			}
		}
		if a.current.CompareAndSwap(cur, next) {
			return nil
		}
	}
}

// Authorize 按最新一代策略对记录的证书链复验一次。
//
// 复验在每次开流时重新进行：重新解析 DER、重新构建到当前信任根的路径、
// 重新检查撤销集，绝不复用 TLS 握手时的缓存结论。
func (a *Authorizer) Authorize(ctx context.Context, recorded [][]byte) (*Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy := a.current.Load()

	if len(recorded) == 0 {
		return a.deny(policy, ErrNoClientCertificate, ""), nil
	}

	peerChain := make([]*x509.Certificate, 0, len(recorded))
	for i, der := range recorded {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return a.deny(policy, ErrMalformedChain, "certificate #"+itoa(i)+": "+err.Error()), nil
		}
		peerChain = append(peerChain, cert)
	}

	verified, err := peerChain[0].Verify(policy.verifyOptions(peerChain))
	if err != nil {
		return a.deny(policy, ErrUntrustedChain, err.Error()), nil
	}

	// 交叉签名可能产生多条可信路径：只要存在一条“全程未撤销”的路径即放行；
	// 仅当每条路径都命中撤销集时才拒绝，从而避免误伤另一条可信路径。
	var revokedDetails []string
	for _, chain := range verified {
		var revokedCert *x509.Certificate
		for _, cert := range chain {
			if policy.IsRevoked(cert) {
				revokedCert = cert
				break
			}
		}
		if revokedCert == nil {
			return &Decision{
				Allowed:        true,
				Generation:     policy.generation,
				Peer:           peerChain[0],
				ChosenChain:    chain,
				VerifiedChains: verified,
			}, nil
		}
		revokedDetails = append(revokedDetails,
			"serial "+revokedCert.SerialNumber.String()+" issued by "+revokedCert.Issuer.String())
	}
	return a.deny(policy, ErrRevoked, strings.Join(revokedDetails, "; ")), nil
}

func (a *Authorizer) deny(policy *Policy, reason error, detail string) *Decision {
	return &Decision{
		Allowed:    false,
		Generation: policy.generation,
		reason:     reason,
		detail:     detail,
	}
}
