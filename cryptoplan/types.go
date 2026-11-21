// Package cryptoplan 为 Go 邮件宿主提供多收件人加密邮件的投递信封规划能力。
//
// 规划输入完全由宿主已验签的数据构成：设备能力声明（DeviceCert）绑定稳定邮箱
// 身份、设备代次、允许算法与有效期；地址别名只能通过带期限的签名边
// （AliasEdge）归并到稳定身份，合并显示名不参与任何证据判断。
//
// 规划器在至多 MaxDeviceGroups 个设备组上求信封数最少的分组方案，并依次按
// 最弱信封算法等级、稳定编号决胜。输出的 Plan 携带完整证书与签名边，可由
// 独立投递端用 VerifyDelivery 复核；提交前用 Recheck 确认证书未吊销、收件组
// 未变更，任何变化都只能重新规划，本包不提供静默降级路径。
package cryptoplan

import (
	"crypto/ed25519"
	"time"
)

// MaxDeviceGroups 是单次规划允许的设备组上限。
const MaxDeviceGroups = 18

// DeviceCert 是设备能力声明证书。设备公钥的 KeyID 由宿主签发时确定，
// 建议使用 KeyIDFromPublicKey 派生。
type DeviceCert struct {
	// CertID 是该证书在宿主体系内的稳定唯一标识，用于吊销检查。
	CertID string `json:"certID"`
	// PrincipalID 是证书绑定的稳定邮箱身份（绝不从显示名派生）。
	PrincipalID string `json:"principalID"`
	// DeviceID 是该身份下的稳定设备标识。
	DeviceID string `json:"deviceID"`
	// Generation 是设备代次，随设备轮换单调递增。
	Generation uint64 `json:"generation"`
	// KeyID 是设备公钥的稳定标识。
	KeyID string `json:"keyID"`
	// PublicKey 是设备公钥的原始字节（如 Ed25519 公钥）。
	PublicKey []byte `json:"publicKey"`
	// Algorithms 是该证书在有效期内授权使用的算法标识集合。
	Algorithms []string `json:"algorithms"`
	// NotBefore 是有效期起点（含）。
	NotBefore time.Time `json:"notBefore"`
	// NotAfter 是有效期终点（不含），与 NotBefore 在同一时间线。
	NotAfter time.Time `json:"notAfter"`
	// Signature 是宿主对证书载荷的 Ed25519 签名。
	Signature []byte `json:"signature"`
}

// AliasEdge 是把邮件地址（别名或转交地址）归并到稳定身份的签名边。
// 边同样具有半开有效期，过期后地址不再具备任何归并证据。
type AliasEdge struct {
	// EdgeID 是边在宿主体系内的稳定唯一标识。
	EdgeID string `json:"edgeID"`
	// Address 是被归并的邮件地址（大小写不敏感比较）。
	Address string `json:"address"`
	// PrincipalID 是地址归并到的稳定邮箱身份。
	PrincipalID string `json:"principalID"`
	// DisplayName 仅用于展示，规划与校验均不把它作为身份证据。
	DisplayName string `json:"displayName"`
	// NotBefore 是有效期起点（含）。
	NotBefore time.Time `json:"notBefore"`
	// NotAfter 是有效期终点（不含）。
	NotAfter time.Time `json:"notAfter"`
	// Signature 是宿主对边载荷的 Ed25519 签名。
	Signature []byte `json:"signature"`
}

// Snapshot 是规划所依据的、宿主已验签的完整身份与证书视图。
type Snapshot struct {
	// HostPublicKey 是签发证书与别名边的宿主验签公钥。
	HostPublicKey ed25519.PublicKey `json:"-"`
	// DeviceCerts 是当前视图中全部（含已过期）设备能力证书。
	DeviceCerts []DeviceCert `json:"deviceCerts"`
	// AliasEdges 是当前视图中全部（含已过期）地址归并边。
	AliasEdges []AliasEdge `json:"aliasEdges"`
}

// DeviceGroup 描述投递请求中一个必须完整覆盖的设备组。
type DeviceGroup struct {
	// StableID 是请求方分配的稳定编号，用于决胜与结果展示，必须唯一。
	StableID string `json:"stableID"`
	// Recipient 是请求中的收件地址，必须能在发布时间解析到稳定身份。
	Recipient string `json:"recipient"`
	// DisplayName 仅用于展示，不参与身份解析。
	DisplayName string `json:"displayName"`
	// DeviceIDs 是该组必须全部覆盖的设备标识（按稳定身份命名空间）。
	DeviceIDs []string `json:"deviceIDs"`
}

// DeliveryRequest 是一次加密投递的规划请求。
type DeliveryRequest struct {
	// Groups 是必须覆盖的设备组，数量不超过 MaxDeviceGroups。
	Groups []DeviceGroup `json:"groups"`
	// ForbiddenPairs 是禁止出现在同一信封内的稳定身份对（如 To 与 BCC）。
	ForbiddenPairs [][2]string `json:"forbiddenPairs"`
	// AlgorithmPreference 是全局算法偏好，靠前的等级更高。
	AlgorithmPreference []string `json:"algorithmPreference"`
	// ReleaseTime 是信封统一的发布时间；每把公钥都必须在该时刻持有授权。
	ReleaseTime time.Time `json:"releaseTime"`
}

// Envelope 是一份物理信封：单算法、面向一组设备组。
type Envelope struct {
	// Index 是信封在方案内的确定序号（按组稳定编号排序后从 0 起）。
	Index int `json:"index"`
	// Algorithm 是该信封选用的唯一算法，取该信封可用的最高等级算法。
	Algorithm string `json:"algorithm"`
	// GroupStableIDs 是该信封覆盖的设备组稳定编号，按字典序排列。
	GroupStableIDs []string `json:"groupStableIDs"`
	// Principals 是信封内披露的稳定身份集合，按字典序排列。
	Principals []string `json:"principals"`
	// Certs 是信封内每把公钥的完整授权证书，供独立投递端逐一校验。
	Certs []DeviceCert `json:"certs"`
	// AliasEdges 是解析本信封收件地址所需的完整签名边。
	AliasEdges []AliasEdge `json:"aliasEdges"`
}

// Plan 是规划器输出的完整投递方案。
type Plan struct {
	// Envelopes 是按稳定编号决胜顺序排列的信封集合。
	Envelopes []Envelope `json:"envelopes"`
	// HostPublicKey 是独立投递端验证全部签名所需的宿主公钥。
	HostPublicKey ed25519.PublicKey `json:"-"`
	// AlgorithmPreference 记录规划时的全局算法偏好（高等级在前）。
	AlgorithmPreference []string `json:"algorithmPreference"`
	// ReleaseTime 是规划采用的统一发布时间。
	ReleaseTime time.Time `json:"releaseTime"`
	// RequestDigest 是规划输入（请求与所用证书/边）的规范摘要，
	// 提交前复检用它确认收件组与证书视图没有变化。
	RequestDigest []byte `json:"requestDigest"`
}

// RevocationChecker 在规划与复检时判断证书是否已被吊销。
// 返回 (true, nil) 表示已吊销；实现应按 CertID 查询宿主吊销清单。
type RevocationChecker func(certID string, releaseTime time.Time) (bool, error)
