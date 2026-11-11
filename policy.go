package netsec

import "fmt"

// Policy combines a vulnerable version set with exact backport exemptions
// for admission checks.
type Policy struct {
	// Vulnerable is the set of versions affected by the vulnerability,
	// typically a union of ranges.
	Vulnerable Set
	// Exemptions holds exact versions that received a backported fix and
	// must not be flagged even though they fall inside Vulnerable.
	Exemptions Set
}

// Verdict is the outcome of an admission check together with the evidence
// explaining it.
type Verdict struct {
	Version    Version
	Vulnerable bool     // version is in the vulnerable set and not exempted
	Exempted   bool     // version matched a vulnerable interval but is exempt
	Interval   Interval // vulnerable interval containing Version, when matched
	Matched    bool     // whether Interval is valid
	Reason     string   // human-readable hit evidence
}

// Check decides whether the locked version v is admitted and records why.
func (p Policy) Check(v Version) Verdict {
	m := p.Vulnerable.Match(v)
	if !m.Hit {
		return Verdict{
			Version: v,
			Reason:  fmt.Sprintf("版本 %s 未命中任何漏洞区间（漏洞集合 %s）", v, p.Vulnerable),
		}
	}
	if p.Exemptions.Contains(v) {
		return Verdict{
			Version:  v,
			Exempted: true,
			Matched:  true,
			Interval: m.Interval,
			Reason:   fmt.Sprintf("版本 %s 命中漏洞区间 %s，但属于精确回移豁免", v, m.Interval),
		}
	}
	return Verdict{
		Version:    v,
		Vulnerable: true,
		Matched:    true,
		Interval:   m.Interval,
		Reason:     fmt.Sprintf("版本 %s 命中漏洞区间 %s", v, m.Interval),
	}
}
