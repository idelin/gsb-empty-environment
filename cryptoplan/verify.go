package cryptoplan

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"
)

// VerifyDelivery 供独立投递端在不接触规划器内部状态的情况下复核整个方案：
// 宿主签名、半开有效期、单算法授权、保密身份隔离、设备组完整覆盖与输入摘要。
func VerifyDelivery(plan *Plan, snap *Snapshot, req *DeliveryRequest,
	revoked RevocationChecker) error {
	if plan == nil || snap == nil || req == nil {
		return fmt.Errorf("%w: 方案、快照与请求均不能为空", ErrInvalidInput)
	}
	if len(plan.HostPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: 方案缺少宿主公钥", ErrInvalidSignature)
	}
	if !bytes.Equal(plan.HostPublicKey, snap.HostPublicKey) {
		return fmt.Errorf("%w: 方案与快照的宿主公钥不一致", ErrInvalidSignature)
	}
	if !plan.ReleaseTime.Equal(req.ReleaseTime) {
		return fmt.Errorf("%w: 发布时间与请求不一致", ErrInputChanged)
	}
	if !sameStringSet(plan.AlgorithmPreference, req.AlgorithmPreference) {
		return fmt.Errorf("%w: 算法偏好与请求不一致", ErrInputChanged)
	}
	rank, err := buildRank(plan.AlgorithmPreference)
	if err != nil {
		return err
	}

	certIndex, edgeIndex, err := verifySnapshotIndex(snap, plan.HostPublicKey)
	if err != nil {
		return err
	}
	alias, err := aliasIndexAt(edgeIndex, req.ReleaseTime)
	if err != nil {
		return err
	}
	groupIndex, err := indexGroups(req)
	if err != nil {
		return err
	}
	forbidden := indexForbidden(req.ForbiddenPairs)
	if err := verifyEnvelopes(plan, rank, certIndex, edgeIndex, alias,
		groupIndex, forbidden, revoked); err != nil {
		return err
	}
	if err := verifyCoverage(plan, groupIndex); err != nil {
		return err
	}
	digest, err := recomputeDigest(plan, snap, req)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, plan.RequestDigest) {
		return fmt.Errorf("%w: 输入摘要不匹配", ErrInputChanged)
	}
	return nil
}

// Recheck 在真正提交前复核方案。证书被吊销或收件组/证书视图发生任何变化，
// 都返回 ErrRevoked 或 ErrInputChanged；调用方只能重新调用 Plan，禁止降级兜底。
func Recheck(plan *Plan, snap *Snapshot, req *DeliveryRequest,
	revoked RevocationChecker) error {
	return VerifyDelivery(plan, snap, req, revoked)
}

func verifySnapshotIndex(snap *Snapshot, hostKey ed25519.PublicKey,
) (map[string]DeviceCert, map[string]AliasEdge, error) {
	certIndex := make(map[string]DeviceCert, len(snap.DeviceCerts))
	for _, cert := range snap.DeviceCerts {
		if err := VerifyDeviceCert(cert, hostKey); err != nil {
			return nil, nil, err
		}
		if _, dup := certIndex[cert.CertID]; dup {
			return nil, nil, fmt.Errorf("%w: 证书 %s 重复", ErrInvalidInput, cert.CertID)
		}
		certIndex[cert.CertID] = cert
	}
	edgeIndex := make(map[string]AliasEdge, len(snap.AliasEdges))
	for _, edge := range snap.AliasEdges {
		if err := VerifyAliasEdge(edge, hostKey); err != nil {
			return nil, nil, err
		}
		if _, dup := edgeIndex[edge.EdgeID]; dup {
			return nil, nil, fmt.Errorf("%w: 别名边 %s 重复", ErrInvalidInput, edge.EdgeID)
		}
		edgeIndex[edge.EdgeID] = edge
	}
	return certIndex, edgeIndex, nil
}

func aliasIndexAt(edgeIndex map[string]AliasEdge, releaseTime time.Time,
) (map[string]AliasEdge, error) {
	index := map[string]AliasEdge{}
	for _, edge := range edgeIndex {
		if !validAt(edge.NotBefore, edge.NotAfter, releaseTime) {
			continue
		}
		address := normalizeAddress(edge.Address)
		if existing, ok := index[address]; ok {
			if existing.PrincipalID != edge.PrincipalID || existing.EdgeID != edge.EdgeID {
				return nil, fmt.Errorf("%w: 地址 %s 存在冲突的有效归并边",
					ErrInvalidInput, address)
			}
		}
		index[address] = edge
	}
	return index, nil
}

func indexGroups(req *DeliveryRequest) (map[string]DeviceGroup, error) {
	out := make(map[string]DeviceGroup, len(req.Groups))
	for _, group := range req.Groups {
		if _, dup := out[group.StableID]; dup {
			return nil, fmt.Errorf("%w: 设备组稳定编号重复: %s",
				ErrInvalidInput, group.StableID)
		}
		out[group.StableID] = group
	}
	return out, nil
}

func indexForbidden(pairs [][2]string) map[[2]string]bool {
	out := make(map[[2]string]bool, len(pairs))
	for _, pair := range pairs {
		left, right := pair[0], pair[1]
		if left == "" || right == "" || left == right {
			continue
		}
		if left > right {
			left, right = right, left
		}
		out[[2]string{left, right}] = true
	}
	return out
}

func verifyEnvelopes(plan *Plan, rank map[string]int,
	certIndex map[string]DeviceCert, edgeIndex map[string]AliasEdge,
	alias map[string]AliasEdge, groupIndex map[string]DeviceGroup,
	forbidden map[[2]string]bool, revoked RevocationChecker) error {
	if len(plan.Envelopes) == 0 {
		return fmt.Errorf("%w: 方案没有信封", ErrInvalidInput)
	}
	groupOwners := map[string]string{}
	seenGroups := map[string]int{}
	for envelopeIndex, envelope := range plan.Envelopes {
		if envelope.Index != envelopeIndex {
			return fmt.Errorf("%w: 信封序号不连续", ErrInvalidInput)
		}
		if _, ok := rank[envelope.Algorithm]; !ok {
			return fmt.Errorf("%w: 信封算法不在偏好列表中", ErrInvalidInput)
		}
		if len(envelope.Certs) == 0 {
			return fmt.Errorf("%w: 信封 %d 未携带完整证书", ErrInvalidInput, envelope.Index)
		}
		principalSet := map[string]struct{}{}
		keyOwners := map[string]string{}
		devices := map[string]struct{}{}
		for _, cert := range envelope.Certs {
			snapshotCert, ok := certIndex[cert.CertID]
			if !ok {
				return fmt.Errorf("%w: 证书 %s 不在宿主快照中", ErrInputChanged, cert.CertID)
			}
			if !certEqual(snapshotCert, cert) {
				return fmt.Errorf("%w: 证书 %s 内容与快照不一致", ErrInputChanged, cert.CertID)
			}
			if !validAt(cert.NotBefore, cert.NotAfter, plan.ReleaseTime) {
				return fmt.Errorf("%w: 证书 %s 在发布时间无效", ErrInvalidInput, cert.CertID)
			}
			if !algAuthorized(cert, envelope.Algorithm) {
				return fmt.Errorf("%w: 证书 %s 未授权算法 %s",
					ErrInvalidInput, cert.CertID, envelope.Algorithm)
			}
			if revoked != nil {
				isRevoked, err := revoked(cert.CertID, plan.ReleaseTime)
				if err != nil {
					return fmt.Errorf("cryptoplan: 吊销查询失败: %w", err)
				}
				if isRevoked {
					return fmt.Errorf("%w: %s", ErrRevoked, cert.CertID)
				}
			}
			if owner, ok := keyOwners[cert.KeyID]; ok && owner != cert.PrincipalID {
				return fmt.Errorf("%w: 公钥 %s 跨身份共用", ErrInvalidInput, cert.KeyID)
			}
			keyOwners[cert.KeyID] = cert.PrincipalID
			principalSet[cert.PrincipalID] = struct{}{}
			devices[cert.PrincipalID+"/"+cert.DeviceID] = struct{}{}
		}
		if err := verifyEnvelopeGroups(envelope, alias, groupIndex,
			groupOwners, seenGroups, devices); err != nil {
			return err
		}
		if err := verifyEnvelopeEdges(envelope, edgeIndex, plan.ReleaseTime); err != nil {
			return err
		}
		principals := sortedStringSet(principalSet)
		for i := 0; i < len(principals); i++ {
			for j := i + 1; j < len(principals); j++ {
				if forbidden[[2]string{principals[i], principals[j]}] {
					return fmt.Errorf("%w: 保密身份对 %s/%s 在同一信封披露",
						ErrInvalidInput, principals[i], principals[j])
				}
			}
		}
		if !sameStringSet(principals, envelope.Principals) {
			return fmt.Errorf("%w: 信封披露身份清单与证书不符", ErrInvalidInput)
		}
	}
	return nil
}

func verifyEnvelopeGroups(envelope Envelope, alias map[string]AliasEdge,
	groupIndex map[string]DeviceGroup, groupOwners map[string]string,
	seenGroups map[string]int, devices map[string]struct{}) error {
	if len(envelope.GroupStableIDs) == 0 {
		return fmt.Errorf("%w: 信封 %d 未覆盖任何设备组", ErrInvalidInput, envelope.Index)
	}
	for _, stableID := range envelope.GroupStableIDs {
		if prior, ok := seenGroups[stableID]; ok {
			return fmt.Errorf("%w: 设备组 %s 同时出现在信封 %d 与 %d",
				ErrInvalidInput, stableID, prior, envelope.Index)
		}
		seenGroups[stableID] = envelope.Index
		group, ok := groupIndex[stableID]
		if !ok {
			return fmt.Errorf("%w: 方案包含未知设备组 %s", ErrInputChanged, stableID)
		}
		edge, ok := alias[normalizeAddress(group.Recipient)]
		if !ok {
			return fmt.Errorf("%w: 设备组 %s 的地址在发布时间无有效归并边",
				ErrNoFeasiblePlan, stableID)
		}
		if owner, ok := groupOwners[stableID]; ok && owner != edge.PrincipalID {
			return fmt.Errorf("%w: 设备组 %s 身份解析不一致", ErrInputChanged, stableID)
		}
		groupOwners[stableID] = edge.PrincipalID
		for _, deviceID := range group.DeviceIDs {
			if _, ok := devices[edge.PrincipalID+"/"+deviceID]; !ok {
				return fmt.Errorf("%w: 设备组 %s 未覆盖设备 %s",
					ErrInputChanged, stableID, deviceID)
			}
		}
	}
	return nil
}

func verifyEnvelopeEdges(envelope Envelope, edgeIndex map[string]AliasEdge,
	releaseTime time.Time) error {
	if len(envelope.AliasEdges) == 0 {
		return fmt.Errorf("%w: 信封 %d 缺少地址归并证据", ErrInvalidInput, envelope.Index)
	}
	edgeSeen := map[string]struct{}{}
	for _, edge := range envelope.AliasEdges {
		snapshotEdge, ok := edgeIndex[edge.EdgeID]
		if !ok {
			return fmt.Errorf("%w: 别名边 %s 不在宿主快照中", ErrInputChanged, edge.EdgeID)
		}
		if !edgeEqual(snapshotEdge, edge) {
			return fmt.Errorf("%w: 别名边 %s 内容与快照不一致", ErrInputChanged, edge.EdgeID)
		}
		if !validAt(edge.NotBefore, edge.NotAfter, releaseTime) {
			return fmt.Errorf("%w: 别名边 %s 在发布时间无效", ErrInvalidInput, edge.EdgeID)
		}
		edgeSeen[edge.EdgeID] = struct{}{}
	}
	return nil
}

func verifyCoverage(plan *Plan, groupIndex map[string]DeviceGroup) error {
	covered := map[string]struct{}{}
	for _, envelope := range plan.Envelopes {
		for _, stableID := range envelope.GroupStableIDs {
			if _, ok := groupIndex[stableID]; !ok {
				return fmt.Errorf("%w: 未知设备组 %s", ErrInputChanged, stableID)
			}
			covered[stableID] = struct{}{}
		}
	}
	if len(covered) != len(groupIndex) {
		return fmt.Errorf("%w: 设备组覆盖数量与请求不一致", ErrInputChanged)
	}
	return nil
}

func recomputeDigest(plan *Plan, snap *Snapshot, req *DeliveryRequest) ([]byte, error) {
	certIDs := map[string]struct{}{}
	edgeIDs := map[string]struct{}{}
	certHash := map[string]string{}
	edgeHash := map[string]string{}
	for _, envelope := range plan.Envelopes {
		for _, cert := range envelope.Certs {
			certIDs[cert.CertID] = struct{}{}
			hash, err := payloadHash(cert.toPayload())
			if err != nil {
				return nil, err
			}
			certHash[cert.CertID] = hash
		}
		for _, edge := range envelope.AliasEdges {
			edgeIDs[edge.EdgeID] = struct{}{}
			hash, err := payloadHash(edge.toPayload())
			if err != nil {
				return nil, err
			}
			edgeHash[edge.EdgeID] = hash
		}
	}
	record := digestRecord{
		Version:   1,
		Request:   *req,
		CertIDs:   sortedStringSet(certIDs),
		EdgeIDs:   sortedStringSet(edgeIDs),
		CertHash:  certHash,
		EdgeHash:  edgeHash,
		HostKeyID: KeyIDFromPublicKey(snap.HostPublicKey),
	}
	raw, err := canonical(record)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func certEqual(a, b DeviceCert) bool {
	ha, _ := payloadHash(a.toPayload())
	hb, _ := payloadHash(b.toPayload())
	return ha == hb && bytes.Equal(a.Signature, b.Signature)
}

func edgeEqual(a, b AliasEdge) bool {
	ha, _ := payloadHash(a.toPayload())
	hb, _ := payloadHash(b.toPayload())
	return ha == hb && bytes.Equal(a.Signature, b.Signature)
}
