package admission

import "slices"

// ReferenceMaxPages 限定参考实现允许的页空间规模。它只服务于小地址域的
// 全历史对照测试，生产路径（Build/Admit）不受此限制且从不按页展开。
const ReferenceMaxPages = 1 << 16

// ReferenceAdmit 是与主实现独立的逐页参考判定：
// 对每个页独立枚举检查点到每个候选根的链，在每条链上逐收据重放，
// 再对所有链取跨历史结论。任何一页都不会借用其它历史的内容或签名。
func ReferenceAdmit(in AdmissionInput) ([]Decision, error) {
	if in.Verifier == nil {
		return nil, ErrMissingVerifier
	}
	if !in.Request.Valid() {
		return nil, ErrBadRequest
	}
	if in.Request.End >= ReferenceMaxPages {
		return nil, ErrBadRequest
	}
	cp := in.Checkpoint
	if err := cp.Valid(); err != nil {
		return nil, err
	}
	byNew := map[string]Receipt{}
	children := map[string][]string{}
	for _, rcpt := range in.Receipts {
		if err := validateReceipt(rcpt, cp.Shards); err != nil {
			return nil, err
		}
		key := string(rcpt.NewRoot)
		if prev, ok := byNew[key]; ok {
			if !sameReceipt(prev, rcpt) {
				return nil, ErrRootConflict
			}
			continue
		}
		byNew[key] = rcpt
		children[string(rcpt.OldRoot)] =
			append(children[string(rcpt.OldRoot)], key)
	}

	heads, err := referenceHeads(in, cp, byNew, children)
	if err != nil {
		return nil, err
	}
	need := in.Need
	if need == 0 {
		need = PermExec
	}

	basePerm := make([]uint8, ReferenceMaxPages)
	for _, pr := range cp.Permissions {
		for p := pr.Pages.Start; p <= pr.Pages.End && p < ReferenceMaxPages; p++ {
			basePerm[p] = pr.Perm
		}
	}

	var decisions []Decision
	flush := func(v Verdict, page uint64) {
		if n := len(decisions); n > 0 &&
			decisions[n-1].Verdict == v &&
			decisions[n-1].Pages.End+1 == page {
			decisions[n-1].Pages.End = page
			return
		}
		decisions = append(decisions, Decision{
			Verdict: v,
			Pages:   Range{Start: page, End: page},
		})
	}

	for page := in.Request.Start; page <= in.Request.End; page++ {
		allSupported, allExec := true, true
		firstDigest, sameDigest := "", true
		for hi, head := range heads {
			chain, err := referenceChain(string(head), string(cp.Root), byNew)
			if err != nil {
				return nil, err
			}
			digest, trust, perm, attested :=
				referencePage(cp, basePerm[page], page, chain, in.Verifier)
			if hi == 0 {
				firstDigest = digest
			} else if digest != firstDigest {
				sameDigest = false
			}
			if !attested || trust != TrustTrusted {
				allSupported = false
			}
			if perm&need != need {
				allExec = false
			}
		}
		switch {
		case allSupported && sameDigest && allExec:
			flush(VerdictExecutable, page)
		case allExec:
			flush(VerdictUnknown, page)
		default:
			flush(VerdictForbidden, page)
		}
	}
	return decisions, nil
}

func referenceHeads(in AdmissionInput, cp Checkpoint,
	byNew map[string]Receipt, children map[string][]string) ([][]byte, error) {
	if len(in.Heads) > 0 {
		heads := make([][]byte, 0, len(in.Heads))
		seen := map[string]bool{}
		for _, h := range in.Heads {
			key := string(h)
			if key != string(cp.Root) {
				if _, ok := byNew[key]; !ok {
					return nil, ErrUnreachable
				}
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			heads = append(heads, slices.Clone(h))
		}
		return heads, nil
	}
	leaf := map[string]bool{}
	for key := range byNew {
		if len(children[key]) == 0 {
			leaf[key] = true
		}
	}
	leaf[string(cp.Root)] = len(children[string(cp.Root)]) == 0
	var heads [][]byte
	for key := range leaf {
		if !leaf[key] {
			continue
		}
		heads = append(heads, []byte(key))
	}
	slices.SortFunc(heads, func(a, b []byte) int { return slices.Compare(a, b) })
	return heads, nil
}

// referenceChain 返回从检查点根到 head 的收据链（不含检查点本身）。
func referenceChain(head, root string,
	byNew map[string]Receipt) ([]Receipt, error) {
	var reversed []Receipt
	key := head
	for key != root {
		rcpt, ok := byNew[key]
		if !ok {
			return nil, ErrUnreachable
		}
		reversed = append(reversed, rcpt)
		key = string(rcpt.OldRoot)
	}
	slices.Reverse(reversed)
	return reversed, nil
}

func referencePage(cp Checkpoint, basePerm uint8, page uint64,
	chain []Receipt, verifier Verifier) (digest string, trust Trust,
	perm uint8, attested bool) {
	perm = basePerm
	for _, bc := range cp.Base {
		if bc.Pages.Contains(page) {
			digest, trust = string(bc.Digest), bc.Trust
			attested = bc.Trust == TrustTrusted
		}
	}
	for _, rcpt := range chain {
		if !rcpt.Pages.Contains(page) {
			continue
		}
		t := verifier.VerifyReceipt(rcpt)
		switch rcpt.Kind {
		case KindContent:
			for _, entry := range rcpt.Content {
				if entry.Pages.Contains(page) {
					digest = string(entry.Digest)
					trust = t
					attested = t == TrustTrusted
				}
			}
		case KindPermission:
			for _, rv := range rcpt.RevokeExec {
				if rv.Contains(page) {
					perm &^= PermExec
				}
			}
		}
	}
	return digest, trust, perm, attested
}
