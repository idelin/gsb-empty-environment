package cryptoplan

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/bits"
	"sort"
)

// eligibleKey 汇总一把在发布时间具备授权的设备公钥。
type eligibleKey struct {
	keyID   string
	algMask uint32
	certs   []DeviceCert
}

// resolvedGroup 是完成身份解析与证书筛选后的内部设备组。
type resolvedGroup struct {
	stableID   string
	recipient  string
	principal  string
	deviceIDs  []string
	principals []string
	keys       []eligibleKey
	groupMask  uint32
	edgeUsed   map[string]AliasEdge
	keyByID    map[string]eligibleKey
}

// feasibility 记录一个设备组子集能否装入单份信封。
type feasibility struct {
	ok       bool
	algMask  uint32
	bestRank int
}

// state 是 DFS 分区问题在某个剩余掩码上的最优结果。
type state struct {
	count      int
	worst      int
	firstBlock uint32
	computed   bool
}

type planner struct {
	req      *DeliveryRequest
	snap     *Snapshot
	revoked  RevocationChecker
	rank     map[string]int
	groups   []*resolvedGroup
	n        int
	conflict [][]bool
	feas     []feasibility
	memo     []state
}

// Plan 依据宿主已验签的快照与投递请求，求信封数最少的投递方案。
//
// 优化顺序固定为：信封数最少 → 最弱信封算法等级最高（worst rank 最小）→
// 按设备组稳定编号的规范分区字典序决胜。revoked 为 nil 时不做吊销查询。
func PlanDelivery(snap *Snapshot, req *DeliveryRequest, revoked RevocationChecker) (*Plan, error) {
	if snap == nil || req == nil {
		return nil, fmt.Errorf("%w: 快照与请求均不能为空", ErrInvalidInput)
	}
	if len(req.Groups) == 0 {
		return nil, fmt.Errorf("%w: 投递请求至少包含一个设备组", ErrInvalidInput)
	}
	if len(req.Groups) > MaxDeviceGroups {
		return nil, fmt.Errorf("%w: 收到 %d 个设备组，上限 %d",
			ErrTooManyGroups, len(req.Groups), MaxDeviceGroups)
	}
	if req.ReleaseTime.IsZero() {
		return nil, fmt.Errorf("%w: 缺少统一发布时间", ErrInvalidInput)
	}
	if len(snap.HostPublicKey) == 0 {
		return nil, fmt.Errorf("%w: 快照缺少宿主验签公钥", ErrInvalidInput)
	}

	rank, err := buildRank(req.AlgorithmPreference)
	if err != nil {
		return nil, err
	}
	p := &planner{
		req:     req,
		snap:    snap,
		revoked: revoked,
		rank:    rank,
	}
	if err := p.verifySnapshotSignatures(); err != nil {
		return nil, err
	}
	if err := p.resolveGroups(); err != nil {
		return nil, err
	}
	p.n = len(p.groups)
	p.buildConflicts()
	p.buildFeasibility()

	full := uint32(1)<<uint(p.n) - 1
	p.solveDP()
	best := p.memo[full]
	if best.count <= 0 {
		return nil, fmt.Errorf("%w: 约束下无法完成分组", ErrNoFeasiblePlan)
	}
	return p.assemble(p.canonical(p.representativeBlocks(full)))
}

func buildRank(preference []string) (map[string]int, error) {
	if len(preference) == 0 {
		return nil, fmt.Errorf("%w: 全局算法偏好不能为空", ErrInvalidInput)
	}
	rank := make(map[string]int, len(preference))
	for i, alg := range preference {
		if alg == "" {
			return nil, fmt.Errorf("%w: 算法偏好存在空标识", ErrInvalidInput)
		}
		if _, dup := rank[alg]; dup {
			return nil, fmt.Errorf("%w: 算法偏好重复: %s", ErrInvalidInput, alg)
		}
		rank[alg] = i
	}
	return rank, nil
}

func (p *planner) verifySnapshotSignatures() error {
	certIDs := make(map[string]struct{}, len(p.snap.DeviceCerts))
	for _, cert := range p.snap.DeviceCerts {
		if err := VerifyDeviceCert(cert, p.snap.HostPublicKey); err != nil {
			return err
		}
		if _, dup := certIDs[cert.CertID]; dup {
			return fmt.Errorf("%w: 证书 CertID 重复: %s", ErrInvalidInput, cert.CertID)
		}
		certIDs[cert.CertID] = struct{}{}
	}
	edgeIDs := make(map[string]struct{}, len(p.snap.AliasEdges))
	for _, edge := range p.snap.AliasEdges {
		if err := VerifyAliasEdge(edge, p.snap.HostPublicKey); err != nil {
			return err
		}
		if _, dup := edgeIDs[edge.EdgeID]; dup {
			return fmt.Errorf("%w: 别名边 EdgeID 重复: %s", ErrInvalidInput, edge.EdgeID)
		}
		edgeIDs[edge.EdgeID] = struct{}{}
	}
	return nil
}

// aliasIndex 收集发布时间有效的签名边；显示名完全不参与解析。
func (p *planner) aliasIndex() (map[string]AliasEdge, error) {
	index := make(map[string]AliasEdge)
	for _, edge := range p.snap.AliasEdges {
		if !validAt(edge.NotBefore, edge.NotAfter, p.req.ReleaseTime) {
			continue
		}
		address := normalizeAddress(edge.Address)
		if existing, ok := index[address]; ok {
			if existing.PrincipalID != edge.PrincipalID {
				return nil, fmt.Errorf("%w: 地址 %s 在发布时间指向多个稳定身份",
					ErrInvalidInput, address)
			}
			if existing.EdgeID != edge.EdgeID {
				return nil, fmt.Errorf("%w: 地址 %s 存在多条有效归并边", ErrInvalidInput, address)
			}
		}
		index[address] = edge
	}
	return index, nil
}

func (p *planner) resolveGroups() error {
	alias, err := p.aliasIndex()
	if err != nil {
		return err
	}
	stableIDs := make(map[string]struct{}, len(p.req.Groups))
	groups := make([]*resolvedGroup, 0, len(p.req.Groups))
	for _, group := range p.req.Groups {
		if group.StableID == "" {
			return fmt.Errorf("%w: 设备组缺少稳定编号", ErrInvalidInput)
		}
		if _, dup := stableIDs[group.StableID]; dup {
			return fmt.Errorf("%w: 设备组稳定编号重复: %s", ErrInvalidInput, group.StableID)
		}
		stableIDs[group.StableID] = struct{}{}
		if len(group.DeviceIDs) == 0 {
			return fmt.Errorf("%w: 设备组 %s 未声明任何设备", ErrInvalidInput, group.StableID)
		}
		address := normalizeAddress(group.Recipient)
		edge, ok := alias[address]
		if !ok {
			return fmt.Errorf("%w: 地址 %s 在发布时间没有有效签名边",
				ErrNoFeasiblePlan, address)
		}
		deviceSet := make(map[string]struct{}, len(group.DeviceIDs))
		for _, deviceID := range group.DeviceIDs {
			if deviceID == "" {
				return fmt.Errorf("%w: 设备组 %s 存在空设备标识", ErrInvalidInput, group.StableID)
			}
			if _, dup := deviceSet[deviceID]; dup {
				return fmt.Errorf("%w: 设备组 %s 设备标识重复: %s",
					ErrInvalidInput, group.StableID, deviceID)
			}
			deviceSet[deviceID] = struct{}{}
		}
		resolved := &resolvedGroup{
			stableID:  group.StableID,
			recipient: address,
			principal: edge.PrincipalID,
			deviceIDs: append([]string(nil), group.DeviceIDs...),
			edgeUsed:  map[string]AliasEdge{edge.EdgeID: edge},
			keyByID:   map[string]eligibleKey{},
		}
		groups = append(groups, resolved)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].stableID < groups[j].stableID })

	for _, group := range groups {
		if err := p.collectKeys(group); err != nil {
			return err
		}
	}
	if err := p.checkKeyOwnership(groups); err != nil {
		return err
	}
	for i, group := range groups {
		group.groupMask = uint32(1) << uint(i)
		group.principals = collectPrincipals(group)
	}
	p.groups = groups
	return nil
}

func (p *planner) collectKeys(group *resolvedGroup) error {
	required := make(map[string]struct{}, len(group.deviceIDs))
	for _, deviceID := range group.deviceIDs {
		required[deviceID] = struct{}{}
	}
	for _, cert := range p.snap.DeviceCerts {
		if cert.PrincipalID != group.principal {
			continue
		}
		if _, wanted := required[cert.DeviceID]; !wanted {
			continue
		}
		if !validAt(cert.NotBefore, cert.NotAfter, p.req.ReleaseTime) {
			continue
		}
		if KeyIDFromPublicKey(cert.PublicKey) != cert.KeyID {
			return fmt.Errorf("%w: 证书 %s 的 KeyID 与公钥不一致", ErrInvalidSignature, cert.CertID)
		}
		if p.revoked != nil {
			revoked, err := p.revoked(cert.CertID, p.req.ReleaseTime)
			if err != nil {
				return fmt.Errorf("cryptoplan: 吊销查询失败: %w", err)
			}
			if revoked {
				continue
			}
		}
		algMask := uint32(0)
		for _, alg := range cert.Algorithms {
			if r, ok := p.rank[alg]; ok {
				algMask |= uint32(1) << uint(r)
			}
		}
		if algMask == 0 {
			continue
		}
		key, ok := group.keyByID[cert.KeyID]
		if !ok {
			key = eligibleKey{keyID: cert.KeyID, algMask: algMask}
		} else {
			key.algMask |= algMask
		}
		key.certs = append(key.certs, cert)
		group.keyByID[cert.KeyID] = key
	}
	for _, deviceID := range group.deviceIDs {
		found := false
		for _, key := range group.keyByID {
			for _, cert := range key.certs {
				if cert.DeviceID == deviceID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: 设备组 %s 的设备 %s 在发布时间无有效授权",
				ErrNoFeasiblePlan, group.stableID, deviceID)
		}
	}
	keyIDs := make([]string, 0, len(group.keyByID))
	for keyID := range group.keyByID {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	group.keys = make([]eligibleKey, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		group.keys = append(group.keys, group.keyByID[keyID])
	}
	return nil
}

// checkKeyOwnership 保证同一把公钥跨组共用时确属同一稳定身份；
// 跨身份的 KeyID 碰撞属于输入冲突，直接拒绝而不是隐式拆分。
func (p *planner) checkKeyOwnership(groups []*resolvedGroup) error {
	owner := make(map[string]string)
	for _, group := range groups {
		for keyID := range group.keyByID {
			if existing, ok := owner[keyID]; ok && existing != group.principal {
				return fmt.Errorf("%w: 公钥 %s 被不同稳定身份声明", ErrInvalidInput, keyID)
			}
			owner[keyID] = group.principal
		}
	}
	return nil
}

func collectPrincipals(group *resolvedGroup) []string {
	set := map[string]struct{}{group.principal: {}}
	for _, key := range group.keys {
		for _, cert := range key.certs {
			set[cert.PrincipalID] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for principal := range set {
		out = append(out, principal)
	}
	sort.Strings(out)
	return out
}

func (p *planner) buildConflicts() {
	n := p.n
	p.conflict = make([][]bool, n)
	for i := range p.conflict {
		p.conflict[i] = make([]bool, n)
	}
	forbidden := make(map[[2]string]bool)
	for _, pair := range p.req.ForbiddenPairs {
		left, right := pair[0], pair[1]
		if left == "" || right == "" || left == right {
			continue
		}
		if left > right {
			left, right = right, left
		}
		forbidden[[2]string{left, right}] = true
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if pairForbidden(p.groups[i].principals, p.groups[j].principals, forbidden) {
				p.conflict[i][j] = true
				p.conflict[j][i] = true
			}
		}
	}
}

func pairForbidden(a, b []string, forbidden map[[2]string]bool) bool {
	for _, x := range a {
		for _, y := range b {
			left, right := x, y
			if left > right {
				left, right = right, left
			}
			if forbidden[[2]string{left, right}] {
				return true
			}
		}
	}
	return false
}

func (p *planner) buildFeasibility() {
	size := 1 << uint(p.n)
	p.feas = make([]feasibility, size)
	p.feas[0] = feasibility{ok: true, algMask: 0, bestRank: -1}
	for mask := uint32(1); mask < uint32(size); mask++ {
		if p.maskHasConflict(mask) {
			p.feas[mask] = feasibility{bestRank: -1}
			continue
		}
		p.feas[mask] = p.feasibilityFrom(mask)
	}
}

// maskHasConflict 判断子集内是否存在任何一对被禁止共存的设备组。
func (p *planner) maskHasConflict(mask uint32) bool {
	for i := 0; i < p.n; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		for j := i + 1; j < p.n; j++ {
			if mask&(1<<uint(j)) != 0 && p.conflict[i][j] {
				return true
			}
		}
	}
	return false
}

// feasibilityFrom 把“所有公钥都支持的算法”作为单信封可选算法集，
// 因此跨公钥逐把求 AND；同一公钥的多张证书授权在收集阶段按 KeyID 求 OR。
func (p *planner) feasibilityFrom(mask uint32) feasibility {
	shared := uint32(0)
	for bit := 0; bit < p.n; bit++ {
		if mask&(1<<uint(bit)) == 0 {
			continue
		}
		groupMask := uint32(0)
		for _, key := range p.groups[bit].keys {
			groupMask |= key.algMask
		}
		if shared == 0 {
			shared = groupMask
		} else {
			shared &= groupMask
		}
		if shared == 0 {
			return feasibility{bestRank: -1}
		}
	}
	if shared == 0 {
		return feasibility{bestRank: -1}
	}
	return feasibility{
		ok:       true,
		algMask:  shared,
		bestRank: lowestSetBit(shared),
	}
}

// lowestSetBit 返回掩码中最低置位的下标；算法偏好位可能不连续，不能直接
// 使用 TrailingZeros32（交集掩码未必包含 rank 0）。
func lowestSetBit(mask uint32) int {
	for rank := 0; mask != 0; rank++ {
		if mask&1 != 0 {
			return rank
		}
		mask >>= 1
	}
	return -1
}

// solveDP 用迭代子集 DP 求每个掩码的最优（信封数最少、最弱等级最高）指标，
// 并在同指标候选中按规范字典序选首块；靠 memo[mask^firstBlock] 即可还原
// 完整决胜分区，全程无递归、无增长切片。锚点固定让每个分区只被计入一次。
func (p *planner) solveDP() {
	size := 1 << uint(p.n)
	p.memo = make([]state, size)
	p.memo[0] = state{count: 0, worst: -1, computed: true}
	for mask := 1; mask < size; mask++ {
		anchor := uint(bits.TrailingZeros32(uint32(mask)))
		anchorBit := uint32(1) << anchor
		rest := uint32(mask) ^ anchorBit
		best := state{count: p.n + 1, worst: 64}
		for sub := rest; ; sub = (sub - 1) & rest {
			block := anchorBit | sub
			entry := p.feas[block]
			if entry.ok {
				child := p.memo[uint32(mask)^block]
				if child.computed && child.count >= 0 {
					count := 1 + child.count
					worst := maxInt(entry.bestRank, child.worst)
					take := false
					switch {
					case count < best.count || (count == best.count && worst < best.worst):
						take = true
					case count == best.count && worst == best.worst:
						// 首块编号序列字典序更小者胜；子分区已是各自最优。
						take = compareMasks(block, best.firstBlock) < 0
					}
					if take {
						best = state{
							count:      count,
							worst:      worst,
							firstBlock: block,
							computed:   true,
						}
					}
				}
			}
			if sub == 0 {
				break
			}
		}
		if best.count > p.n {
			best = state{count: -1, computed: true}
		}
		p.memo[mask] = best
	}
}

// representativeBlocks 按 memo 里的首块链还原一个代表分区。
func (p *planner) representativeBlocks(mask uint32) []uint32 {
	var blocks []uint32
	for mask != 0 {
		block := p.memo[mask].firstBlock
		blocks = append(blocks, block)
		mask ^= block
	}
	return blocks
}

// canonical 把分区块按“块内最小组编号”排序，使决胜只取决于稳定编号顺序。
func (p *planner) canonical(blocks []uint32) []uint32 {
	out := append([]uint32(nil), blocks...)
	sort.Slice(out, func(i, j int) bool {
		return bits.TrailingZeros32(out[i]) < bits.TrailingZeros32(out[j])
	})
	return out
}

// compareMasks 把位掩码看作升序编号序列做字典序比较；
// 一个序列是另一个的前缀时，较短者胜。
func compareMasks(a, b uint32) int {
	for a != 0 || b != 0 {
		if a == 0 {
			return -1
		}
		if b == 0 {
			return 1
		}
		la := bits.TrailingZeros32(a)
		lb := bits.TrailingZeros32(b)
		if la != lb {
			return la - lb
		}
		a ^= uint32(1) << uint(la)
		b ^= uint32(1) << uint(lb)
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type digestRecord struct {
	Version   int               `json:"version"`
	Request   DeliveryRequest   `json:"request"`
	CertIDs   []string          `json:"certIDs"`
	EdgeIDs   []string          `json:"edgeIDs"`
	CertHash  map[string]string `json:"certHash"`
	EdgeHash  map[string]string `json:"edgeHash"`
	HostKeyID string            `json:"hostKeyID"`
}

func (p *planner) assemble(blocks []uint32) (*Plan, error) {
	blocks = p.canonical(blocks)
	envelopes := make([]Envelope, 0, len(blocks))
	allCertIDs := map[string]struct{}{}
	allEdgeIDs := map[string]struct{}{}
	certHash := map[string]string{}
	edgeHash := map[string]string{}

	for index, block := range blocks {
		entry := p.feas[block]
		if !entry.ok {
			return nil, fmt.Errorf("%w: 内部错误，选中了不可行信封", ErrNoFeasiblePlan)
		}
		algorithm := p.req.AlgorithmPreference[entry.bestRank]

		groupIDs := make([]string, 0, bits.OnesCount32(block))
		principalSet := map[string]struct{}{}
		certSet := map[string]DeviceCert{}
		edgeSet := map[string]AliasEdge{}
		for bit := 0; bit < p.n; bit++ {
			if block&(1<<uint(bit)) == 0 {
				continue
			}
			group := p.groups[bit]
			groupIDs = append(groupIDs, group.stableID)
			principalSet[group.principal] = struct{}{}
			for _, key := range group.keys {
				for _, cert := range key.certs {
					if !algAuthorized(cert, algorithm) {
						continue
					}
					certSet[cert.CertID] = cert
					allCertIDs[cert.CertID] = struct{}{}
					hash, err := payloadHash(cert.toPayload())
					if err != nil {
						return nil, err
					}
					certHash[cert.CertID] = hash
				}
			}
			for edgeID, edge := range group.edgeUsed {
				edgeSet[edgeID] = edge
				allEdgeIDs[edgeID] = struct{}{}
				hash, err := payloadHash(edge.toPayload())
				if err != nil {
					return nil, err
				}
				edgeHash[edgeID] = hash
			}
		}
		if len(certSet) == 0 {
			return nil, fmt.Errorf("%w: 信封缺少授权证书", ErrNoFeasiblePlan)
		}
		envelopes = append(envelopes, Envelope{
			Index:          index,
			Algorithm:      algorithm,
			GroupStableIDs: groupIDs,
			Principals:     sortedKeys(principalSet),
			Certs:          sortedCerts(certSet),
			AliasEdges:     sortedEdges(edgeSet),
		})
	}

	digest, err := p.buildDigest(allCertIDs, allEdgeIDs, certHash, edgeHash)
	if err != nil {
		return nil, err
	}
	return &Plan{
		Envelopes:           envelopes,
		HostPublicKey:       append(ed25519.PublicKey(nil), p.snap.HostPublicKey...),
		AlgorithmPreference: append([]string(nil), p.req.AlgorithmPreference...),
		ReleaseTime:         p.req.ReleaseTime.UTC(),
		RequestDigest:       digest,
	}, nil
}

func algAuthorized(cert DeviceCert, algorithm string) bool {
	for _, alg := range cert.Algorithms {
		if alg == algorithm {
			return true
		}
	}
	return false
}

func payloadHash(v any) (string, error) {
	raw, err := canonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (p *planner) buildDigest(certIDs, edgeIDs map[string]struct{},
	certHash, edgeHash map[string]string) ([]byte, error) {
	record := digestRecord{
		Version:   1,
		Request:   *p.req,
		CertIDs:   sortedStringSet(certIDs),
		EdgeIDs:   sortedStringSet(edgeIDs),
		CertHash:  certHash,
		EdgeHash:  edgeHash,
		HostKeyID: KeyIDFromPublicKey(p.snap.HostPublicKey),
	}
	raw, err := canonical(record)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

func sortedStringSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	return sortedStringSet(set)
}

func sortedCerts(set map[string]DeviceCert) []DeviceCert {
	out := make([]DeviceCert, 0, len(set))
	for _, cert := range set {
		out = append(out, cert)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CertID < out[j].CertID })
	return out
}

func sortedEdges(set map[string]AliasEdge) []AliasEdge {
	out := make([]AliasEdge, 0, len(set))
	for _, edge := range set {
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}
