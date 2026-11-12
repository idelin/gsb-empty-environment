package netsec

import (
	"sort"
	"strconv"
)

func isDeleteDS(d DSRecord) bool {
	return d.KeyTag == 0 && d.Algorithm == 0 && d.DigestType == 0 && len(d.Digest) == 1 && d.Digest[0] == 0
}

func isDeleteDNSKEY(k DNSKEYRecord) bool {
	return k.Flags == 0 && k.Protocol == 3 && k.Algorithm == 0 &&
		len(k.PublicKey) == 1 && k.PublicKey[0] == 0
}

func allDeleteDS(cds []DSRecord) bool {
	for _, d := range cds {
		if !isDeleteDS(d) {
			return false
		}
	}
	return true
}

func allDeleteDNSKEY(keys []DNSKEYRecord) bool {
	for _, k := range keys {
		if !isDeleteDNSKEY(k) {
			return false
		}
	}
	return true
}

// validateCDSStructure 强制删除信号不得与其他记录混放，且删除记录必须
// 严格等于规范的 0 0 0 00（防止 0 0 0 "" 之类的变体混入）。
func validateCDSStructure(cds []DSRecord) error {
	deleteSeen := false
	for _, d := range cds {
		if isDeleteDS(d) {
			deleteSeen = true
			continue
		}
		if deleteSeen || looksLikePartialDeleteDS(d) {
			return ruleErr(RuleDeleteMixed, "delete CDS mixed with other DS records", ErrDeleteMixed)
		}
		if d.Algorithm == 0 || d.DigestType == 0 || len(d.Digest) == 0 {
			return ruleErr(RuleDeleteMalformed, "non-delete CDS has zero algorithm/digest or empty digest", ErrDeleteMalformed)
		}
	}
	if deleteSeen && len(cds) > 1 {
		return ruleErr(RuleDeleteMixed, "delete CDS mixed with other DS records", ErrDeleteMixed)
	}
	return nil
}

func validateCDNSKEYStructure(keys []DNSKEYRecord) error {
	deleteSeen := false
	for _, k := range keys {
		if isDeleteDNSKEY(k) {
			deleteSeen = true
			continue
		}
		if deleteSeen || (k.Flags == 0 && k.Algorithm == 0) {
			return ruleErr(RuleDeleteMixed, "delete CDNSKEY mixed with other DNSKEY records", ErrDeleteMixed)
		}
		if k.Protocol != 3 {
			return ruleErr(RuleMalformed, "DNSKEY protocol field must be 3", ErrMalformed)
		}
		if k.Algorithm == 0 || len(k.PublicKey) == 0 {
			return ruleErr(RuleDeleteMalformed, "non-delete CDNSKEY has zero algorithm or empty key", ErrDeleteMalformed)
		}
	}
	if deleteSeen && len(keys) > 1 {
		return ruleErr(RuleDeleteMixed, "delete CDNSKEY mixed with other DNSKEY records", ErrDeleteMixed)
	}
	return nil
}

// looksLikePartialDeleteDS 捕获字段部分为零的“准删除”记录，避免它被当成
// 普通 DS 蒙混过关。
func looksLikePartialDeleteDS(d DSRecord) bool {
	zeroFields := 0
	if d.KeyTag == 0 {
		zeroFields++
	}
	if d.Algorithm == 0 {
		zeroFields++
	}
	if d.DigestType == 0 {
		zeroFields++
	}
	if len(d.Digest) == 0 || (len(d.Digest) == 1 && d.Digest[0] == 0) {
		zeroFields++
	}
	return zeroFields > 0 && zeroFields < 4
}

// fingerprint 生成记录集的规范指纹：带 RR 类型前缀的长度前缀 RDATA 序列，
// RDATA 按规范排序，因此顺序差异不影响一致性判断。
func fingerprint(cds []DSRecord, cdns []DNSKEYRecord) string {
	var b []byte
	if len(cds) > 0 {
		sorted := cloneDS(cds)
		sort.Slice(sorted, func(i, j int) bool {
			return canonicalRRLess(dsRDATA(sorted[i]), dsRDATA(sorted[j]))
		})
		b = append(b, byte(TypeCDS>>8), byte(TypeCDS))
		for _, d := range sorted {
			rdata := dsRDATA(d)
			b = append(b, byte(len(rdata)>>8), byte(len(rdata)))
			b = append(b, rdata...)
		}
	}
	if len(cdns) > 0 {
		sorted := cloneDNSKEY(cdns)
		sort.Slice(sorted, func(i, j int) bool {
			return canonicalRRLess(dnskeyRDATA(sorted[i]), dnskeyRDATA(sorted[j]))
		})
		b = append(b, byte(TypeCDNSKEY>>8), byte(TypeCDNSKEY))
		for _, k := range sorted {
			rdata := dnskeyRDATA(k)
			b = append(b, byte(len(rdata)>>8), byte(len(rdata)))
			b = append(b, rdata...)
		}
	}
	return string(b)
}

// deriveDS 从 CDNSKEY 集推导父区应发布的 DS 集（仅含 KSK）。
func deriveDS(zone string, keys []DNSKEYRecord, digestType uint8) ([]DSRecord, error) {
	out := make([]DSRecord, 0, len(keys))
	for _, k := range keys {
		if !k.IsKSK() {
			continue
		}
		digest, err := DSDigest(zone, k, digestType)
		if err != nil {
			return nil, ruleErr(RuleNewDSNoDNSKEY, "cannot build DS digest from CDNSKEY", err)
		}
		out = append(out, DSRecord{
			KeyTag:     KeyTag(k),
			Algorithm:  k.Algorithm,
			DigestType: digestType,
			Digest:     digest,
		})
	}
	if len(out) == 0 {
		return nil, ruleErr(RuleMalformed, "CDNSKEY set contains no KSK (SEP) record", ErrMalformed)
	}
	return out, nil
}

// validateCorrespondence 要求并存的 CDS 与 CDNSKEY 可互相推导：用 CDS 中
// 出现的每种摘要类型从 CDNSKEY 完整推导 DS 集，二者须逐字节相同。
func validateCorrespondence(zone string, cds []DSRecord, cdns []DNSKEYRecord) error {
	digestTypes := map[uint8]struct{}{}
	for _, d := range cds {
		digestTypes[d.DigestType] = struct{}{}
	}
	derived := make([]DSRecord, 0, len(cdns)*len(digestTypes))
	for _, k := range cdns {
		for digestType := range digestTypes {
			digest, err := DSDigest(zone, k, digestType)
			if err != nil {
				return ruleErr(RuleCDSCDNSKEYMismatch, "cannot derive DS digest from CDNSKEY", err)
			}
			derived = append(derived, DSRecord{
				KeyTag:     KeyTag(k),
				Algorithm:  k.Algorithm,
				DigestType: digestType,
				Digest:     digest,
			})
		}
	}
	sort.Slice(derived, func(i, j int) bool {
		return canonicalRRLess(dsRDATA(derived[i]), dsRDATA(derived[j]))
	})
	sortedCDS := cloneDS(cds)
	sort.Slice(sortedCDS, func(i, j int) bool {
		return canonicalRRLess(dsRDATA(sortedCDS[i]), dsRDATA(sortedCDS[j]))
	})
	if len(derived) != len(sortedCDS) {
		return ruleErr(RuleCDSCDNSKEYMismatch,
			"CDS/CDNSKEY derive different numbers of DS records", ErrCDSCDNSKEYMismatch)
	}
	for i := range derived {
		if !bytesEqual(dsRDATA(derived[i]), dsRDATA(sortedCDS[i])) {
			return ruleErr(RuleCDSCDNSKEYMismatch,
				"CDS record "+strconv.Itoa(i)+" is not derivable from the CDNSKEY set",
				ErrCDSCDNSKEYMismatch)
		}
	}
	return nil
}

func dsAsRecords(cds []DSRecord) []DNSRecord {
	out := make([]DNSRecord, len(cds))
	for i := range cds {
		out[i] = cds[i]
	}
	return out
}

func dnskeyAsRecords(keys []DNSKEYRecord) []DNSRecord {
	out := make([]DNSRecord, len(keys))
	for i := range keys {
		out[i] = keys[i]
	}
	return out
}

func cloneDS(in []DSRecord) []DSRecord {
	out := make([]DSRecord, len(in))
	for i, d := range in {
		out[i] = d
		if d.Digest != nil {
			out[i].Digest = append([]byte(nil), d.Digest...)
		}
	}
	return out
}

func cloneDNSKEY(in []DNSKEYRecord) []DNSKEYRecord {
	out := make([]DNSKEYRecord, len(in))
	for i, k := range in {
		out[i] = k
		if k.PublicKey != nil {
			out[i].PublicKey = append([]byte(nil), k.PublicKey...)
		}
	}
	return out
}

func itoa(v uint16) string { return strconv.Itoa(int(v)) }
