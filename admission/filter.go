package admission

import "sort"

// Admit 对单个公钥更新包执行准入过滤。它不修改输入，返回裁剪后的新包、
// 墓碑摘要集合和统计信息；仅当裁剪后体积超过 1 MiB 或参数非法时返回错误。
func Admit(pk *PublicKeyPacket, opts Options) (*Result, error) {
	return filterPacket(pk, opts, nil)
}

// verifyGate 封装预算与回调验证。
type verifyGate struct {
	opts  Options
	stats *Stats
}

// check 执行一次验证。deferrable 为 true 表示预算已尽、签名尚未验证，
// 调用方应静默丢弃（不写墓碑）以便后续预算充足时重试。
func (g *verifyGate) check(pk *PublicKeyPacket, uid UserID, sig Signature) (ok bool, deferrable bool) {
	budget := g.opts.Budget
	if budget == 0 {
		return false, true
	}
	if budget > 0 && g.stats.VerificationsUsed >= budget {
		return false, true
	}
	g.stats.VerificationsUsed++
	if sig.Self {
		g.stats.SelfSigsVerified++
	} else {
		g.stats.ThirdPartyVerified++
	}
	return g.opts.Verify(pk, uid, sig), false
}

// filterPacket 是核心过滤流程。base 为已存在的墓碑集合（合并场景），
// 本函数在 base 的副本上累积，绝不原地修改 base。
func filterPacket(pk *PublicKeyPacket, opts Options, base map[string]struct{}) (*Result, error) {
	if pk == nil {
		return nil, errInvalid("nil public-key packet")
	}
	if opts.Verify == nil {
		return nil, errInvalid("nil verify callback")
	}
	serialize := opts.Serialize
	if serialize == nil {
		serialize = DefaultSerializer
	}

	tombstones := make(map[string]struct{})
	for digest := range base {
		tombstones[digest] = struct{}{}
	}
	baseCount := len(tombstones)

	stats := Stats{UIDsIn: len(pk.UserIDs), SubkeysIn: len(pk.Subkeys)}
	gate := &verifyGate{opts: opts, stats: &stats}
	tombstone := func(sig Signature) {
		if len(sig.Raw) == 0 {
			return
		}
		tombstones[Digest(sig)] = struct{}{}
	}

	type processedUID struct{ uid UserID }
	var processed []processedUID

	for _, uid := range pk.UserIDs {
		stats.ThirdPartyIn += countThirdParty(uid.Signatures)

		var selfSigs, thirdParty []Signature
		for _, sig := range uid.Signatures {
			if sig.Self {
				selfSigs = append(selfSigs, sig)
			} else {
				thirdParty = append(thirdParty, sig)
			}
		}

		// 自签名按创建时间降序，取最新一条可验证者。
		sort.SliceStable(selfSigs, func(i, j int) bool {
			return newerFirst(selfSigs[i], selfSigs[j])
		})

		var chosenSelf *Signature
		deferredSelf := false
		for _, sig := range selfSigs {
			if len(sig.Raw) == 0 {
				tombstone(sig)
				continue
			}
			ok, deferrable := gate.check(pk, uid, sig)
			if deferrable {
				deferredSelf = true
				continue
			}
			if ok {
				s := sig
				chosenSelf = &s
				break
			}
			stats.SelfSigsRejected++
			tombstone(sig)
		}

		if chosenSelf == nil {
			// 没有任何自签名得到验证，整个用户标识丢弃。
			if deferredSelf {
				// 预算原因：静默丢弃，不写墓碑，允许后续更新重试。
				for _, sig := range uid.Signatures {
					if !sig.Self {
						stats.ThirdPartyDeferred++
					}
				}
			} else {
				// 伪造/畸形自签名：用户标识丢弃，其上全部签名写入墓碑，
				// 防止后续更新回灌。
				for _, sig := range uid.Signatures {
					tombstone(sig)
					if !sig.Self {
						stats.ThirdPartyRejected++
					}
				}
			}
			stats.UIDsDropped++
			continue
		}

		// 更旧的自签名一律丢弃并墓碑化，仅保留选中的最新有效自签名。
		chosenDigest := Digest(*chosenSelf)
		for _, sig := range selfSigs {
			if len(sig.Raw) == 0 || Digest(sig) == chosenDigest {
				continue
			}
			tombstone(sig)
		}

		keptThird := filterThirdParty(pk, uid, thirdParty, opts, tombstones, gate, &stats)

		newUID := UserID{
			Raw:        append([]byte(nil), uid.Raw...),
			Created:    uid.Created,
			Signatures: append([]Signature{*chosenSelf}, keptThird...),
		}
		processed = append(processed, processedUID{uid: newUID})
	}

	// 用户标识按创建时间降序，最多保留 MaxUIDs 个；超出部分整体丢弃，
	// 其签名写入墓碑（裁剪而非拒绝整个公钥）。
	sort.SliceStable(processed, func(i, j int) bool {
		a, b := processed[i].uid, processed[j].uid
		if !a.Created.Equal(b.Created) {
			return a.Created.After(b.Created)
		}
		return uidDigest(a) < uidDigest(b)
	})
	if len(processed) > MaxUIDs {
		for _, item := range processed[MaxUIDs:] {
			for _, sig := range item.uid.Signatures {
				tombstone(sig)
				if !sig.Self {
					stats.ThirdPartyCapped++
				}
			}
		}
		processed = processed[:MaxUIDs]
	}

	keptUIDs := make([]UserID, 0, len(processed))
	for _, item := range processed {
		keptUIDs = append(keptUIDs, item.uid)
	}
	stats.UIDsKept = len(keptUIDs)

	// 子密钥按创建时间降序，最多保留 MaxSubkeys 个。
	subkeys := append([]Subkey(nil), pk.Subkeys...)
	sort.SliceStable(subkeys, func(i, j int) bool {
		if !subkeys[i].Created.Equal(subkeys[j].Created) {
			return subkeys[i].Created.After(subkeys[j].Created)
		}
		return string(subkeys[i].Raw) < string(subkeys[j].Raw)
	})
	if len(subkeys) > MaxSubkeys {
		subkeys = subkeys[:MaxSubkeys]
	}
	stats.SubkeysKept = len(subkeys)

	filtered := &PublicKeyPacket{
		Fingerprint: pk.Fingerprint,
		PrimaryRaw:  append([]byte(nil), pk.PrimaryRaw...),
		UserIDs:     keptUIDs,
		Subkeys:     subkeys,
	}

	raw, err := serialize(filtered)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxPacketSize {
		return nil, ErrPacketTooLarge
	}

	stats.TombstonesAdded = len(tombstones) - baseCount
	return &Result{Packet: filtered, Tombstones: tombstones, Stats: stats}, nil
}

// filterThirdParty 处理单个用户标识上的第三方签名：
// 墓碑/重复/非白名单/验证失败/超上限者丢弃，最多保留
// MaxThirdPartyPerUID 条按创建时间较新的有效白名单签名。
func filterThirdParty(
	pk *PublicKeyPacket,
	uid UserID,
	sigs []Signature,
	opts Options,
	tombstones map[string]struct{},
	gate *verifyGate,
	stats *Stats,
) []Signature {
	// 按创建时间降序排列，保证“较新”的签名优先进入验证与保留窗口。
	sort.SliceStable(sigs, func(i, j int) bool {
		return newerFirst(sigs[i], sigs[j])
	})

	seen := make(map[string]struct{})
	var kept []Signature

	for _, sig := range sigs {
		if len(sig.Raw) == 0 {
			// 畸形签名（无内容）无法形成摘要，直接拒绝。
			stats.ThirdPartyRejected++
			continue
		}
		digest := Digest(sig)

		// 已在墓碑中（历史上被丢弃）：永远不得加回。
		if _, dead := tombstones[digest]; dead {
			// 同次输入里再次出现的重复项已在首次出现时写入墓碑，这里
			// 计入去重计数，使统计反映全部重复输入。
			if _, dup := seen[digest]; dup {
				stats.ThirdPartyDuplicate++
			} else {
				stats.ThirdPartyRejected++
			}
			continue
		}
		// 同次更新内的内容摘要去重。
		if _, dup := seen[digest]; dup {
			stats.ThirdPartyDuplicate++
			tombstones[digest] = struct{}{}
			continue
		}

		// 非白名单签名者：拒绝并墓碑化，不消耗验证预算。
		if !opts.Whitelist.Allowed(sig.Issuer) {
			stats.ThirdPartyRejected++
			tombstones[digest] = struct{}{}
			continue
		}

		// 超过每用户标识 50 条上限：较旧者墓碑化（去重与白名单之后，
		// 避免把重复/垃圾签名计入有效上限）。
		if len(kept) >= MaxThirdPartyPerUID {
			stats.ThirdPartyCapped++
			tombstones[digest] = struct{}{}
			continue
		}

		ok, deferrable := gate.check(pk, uid, sig)
		if deferrable {
			// 预算耗尽：静默丢弃，不写墓碑，允许后续重试。
			stats.ThirdPartyDeferred++
			continue
		}
		if !ok {
			stats.ThirdPartyRejected++
			tombstones[digest] = struct{}{}
			continue
		}

		seen[digest] = struct{}{}
		kept = append(kept, sig)
	}

	return kept
}

type invalidError string

func (e invalidError) Error() string { return "admission: " + string(e) }

func errInvalid(msg string) error { return invalidError(msg) }
