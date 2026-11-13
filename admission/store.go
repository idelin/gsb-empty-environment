package admission

// StoredKey 是密钥服务器中已保存的一把公钥及其墓碑集合。
type StoredKey struct {
	// Packet 是当前已保存（已准入）的公钥包。
	Packet *PublicKeyPacket
	// Tombstones 是历史上被丢弃签名的内容摘要集合，永久阻止这些签名回灌。
	Tombstones map[string]struct{}
}

// Store 是内存态的已存公钥集合，演示合并语义。生产环境可将其替换为
// 数据库支撑的实现，只要保持 Merge 的过滤与墓碑规则不变即可。
// Store 的方法不是并发安全的，调用方需自行加锁。
type Store struct {
	keys map[string]*StoredKey
}

// NewStore 创建空的合并存储。
func NewStore() *Store {
	return &Store{keys: make(map[string]*StoredKey)}
}

// Get 返回指纹对应的已存公钥；不存在时返回 nil。
func (s *Store) Get(fingerprint string) *StoredKey {
	return s.keys[fingerprint]
}

// Len 返回已存公钥数量。
func (s *Store) Len() int { return len(s.keys) }

// Merge 把一个更新包准入后合并进已存公钥。
//
// 流程：
//  1. 若已存在同指纹公钥，先把更新包与已存包合并（按内容摘要去重，
//     过滤掉墓碑中的签名），再走一次完整准入；
//  2. 准入产生的墓碑累积到该公钥的墓碑集合，保证被丢弃的签名不会因
//     后续更新被加回；
//  3. 指纹不一致或体积超限时返回错误且不修改已有状态。
func (s *Store) Merge(incoming *PublicKeyPacket, opts Options) (*Result, error) {
	if incoming == nil {
		return nil, errInvalid("nil public-key packet")
	}
	existing := s.keys[incoming.Fingerprint]

	var candidate *PublicKeyPacket
	var baseTombstones map[string]struct{}
	if existing == nil {
		candidate = clonePacket(incoming)
		baseTombstones = nil
	} else {
		candidate = unionPackets(existing.Packet, incoming, existing.Tombstones)
		baseTombstones = existing.Tombstones
	}

	result, err := filterPacket(candidate, opts, baseTombstones)
	if err != nil {
		return nil, err
	}

	s.keys[incoming.Fingerprint] = &StoredKey{
		Packet:     result.Packet,
		Tombstones: result.Tombstones,
	}
	return result, nil
}

// MergeInto 把更新包合并进指定的已存公钥并重新准入。当两者主指纹不一致
// 时返回 ErrFingerprintMismatch，适用于调用方已持有 StoredKey 的场景。
func MergeInto(existing *StoredKey, incoming *PublicKeyPacket, opts Options) (*Result, error) {
	if existing == nil || existing.Packet == nil {
		return filterPacket(clonePacket(incoming), opts, nil)
	}
	if incoming == nil {
		return nil, errInvalid("nil public-key packet")
	}
	if existing.Packet.Fingerprint != incoming.Fingerprint {
		return nil, ErrFingerprintMismatch
	}
	candidate := unionPackets(existing.Packet, incoming, existing.Tombstones)
	return filterPacket(candidate, opts, existing.Tombstones)
}

// unionPackets 合并已存包与更新包：主密钥以已存为准，用户标识按 Raw
// 内容归并（签名按摘要去重并剔除墓碑签名），子密钥按 Raw 去重。
func unionPackets(existing, incoming *PublicKeyPacket, tombstones map[string]struct{}) *PublicKeyPacket {
	merged := &PublicKeyPacket{
		Fingerprint: incoming.Fingerprint,
		PrimaryRaw:  append([]byte(nil), incoming.PrimaryRaw...),
	}
	if existing != nil && len(existing.PrimaryRaw) > 0 {
		merged.PrimaryRaw = append([]byte(nil), existing.PrimaryRaw...)
	}

	uidIndex := make(map[string]int)
	appendUID := func(uid UserID) {
		key := string(uid.Raw)
		if idx, ok := uidIndex[key]; ok {
			merged.UserIDs[idx].Signatures = mergeSignatures(
				merged.UserIDs[idx].Signatures, uid.Signatures, tombstones)
			return
		}
		uidIndex[key] = len(merged.UserIDs)
		merged.UserIDs = append(merged.UserIDs, UserID{
			Raw:        append([]byte(nil), uid.Raw...),
			Created:    uid.Created,
			Signatures: mergeSignatures(nil, uid.Signatures, tombstones),
		})
	}

	if existing != nil {
		for _, uid := range existing.UserIDs {
			appendUID(uid)
		}
	}
	for _, uid := range incoming.UserIDs {
		appendUID(uid)
	}

	subSeen := make(map[string]struct{})
	appendSub := func(sub Subkey) {
		key := string(sub.Raw)
		if _, ok := subSeen[key]; ok {
			return
		}
		subSeen[key] = struct{}{}
		merged.Subkeys = append(merged.Subkeys, Subkey{
			Raw:     append([]byte(nil), sub.Raw...),
			Created: sub.Created,
		})
	}
	if existing != nil {
		for _, sub := range existing.Subkeys {
			appendSub(sub)
		}
	}
	for _, sub := range incoming.Subkeys {
		appendSub(sub)
	}

	return merged
}

// mergeSignatures 把新签名并入已有签名，按内容摘要去重，并剔除墓碑签名。
func mergeSignatures(base, extra []Signature, tombstones map[string]struct{}) []Signature {
	seen := make(map[string]struct{}, len(base))
	out := make([]Signature, 0, len(base)+len(extra))
	add := func(sig Signature) {
		if len(sig.Raw) == 0 {
			return
		}
		digest := Digest(sig)
		if _, dead := tombstones[digest]; dead {
			return
		}
		if _, dup := seen[digest]; dup {
			return
		}
		seen[digest] = struct{}{}
		out = append(out, sig)
	}
	for _, sig := range base {
		add(sig)
	}
	for _, sig := range extra {
		add(sig)
	}
	return out
}

// clonePacket 深拷贝一个公钥包，保证过滤不修改调用方输入。
func clonePacket(pk *PublicKeyPacket) *PublicKeyPacket {
	if pk == nil {
		return nil
	}
	out := &PublicKeyPacket{
		Fingerprint: pk.Fingerprint,
		PrimaryRaw:  append([]byte(nil), pk.PrimaryRaw...),
		Subkeys:     make([]Subkey, 0, len(pk.Subkeys)),
		UserIDs:     make([]UserID, 0, len(pk.UserIDs)),
	}
	for _, sub := range pk.Subkeys {
		out.Subkeys = append(out.Subkeys, Subkey{
			Raw:     append([]byte(nil), sub.Raw...),
			Created: sub.Created,
		})
	}
	for _, uid := range pk.UserIDs {
		copied := UserID{
			Raw:        append([]byte(nil), uid.Raw...),
			Created:    uid.Created,
			Signatures: make([]Signature, 0, len(uid.Signatures)),
		}
		for _, sig := range uid.Signatures {
			copied.Signatures = append(copied.Signatures, Signature{
				Raw:     append([]byte(nil), sig.Raw...),
				Issuer:  sig.Issuer,
				Created: sig.Created,
				Self:    sig.Self,
			})
		}
		out.UserIDs = append(out.UserIDs, copied)
	}
	return out
}
