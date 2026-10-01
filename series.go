package cardinalguard

import (
	"fmt"
	"sort"
	"strings"
)

// ReservedPrefix marks label keys owned by the limiter itself. Real series
// may not use them, which guarantees the synthetic overflow bucket identity
// can never collide with a real label combination.
const ReservedPrefix = "__"

// OverflowLabel is the injected label on the synthetic overflow bucket.
const OverflowLabel = "__overflow__"

// LabelSet is one label combination. Combination identity is
// order-insensitive: canonicalKey sorts label keys before encoding.
type LabelSet map[string]string

// ValidateLabels rejects label combinations that could collide with the
// reserved overflow bucket identity or break the canonical encoding.
func ValidateLabels(labels LabelSet) error {
	for k, v := range labels {
		if k == "" {
			return fmt.Errorf("empty label key")
		}
		if strings.HasPrefix(k, ReservedPrefix) {
			return fmt.Errorf("label key %q uses reserved prefix %q", k, ReservedPrefix)
		}
		if strings.ContainsRune(k, 0) || strings.ContainsRune(v, 0) {
			return fmt.Errorf("label %q contains NUL", k)
		}
	}
	return nil
}

func validateScope(tenant, metric string) error {
	if tenant == "" || metric == "" {
		return fmt.Errorf("tenant and metric must be non-empty")
	}
	if strings.ContainsRune(tenant, 0) || strings.ContainsRune(metric, 0) {
		return fmt.Errorf("tenant/metric contains NUL")
	}
	return nil
}

// canonicalKey encodes tenant+metric+labels with sorted label keys so that
// {a=1,b=2} and {b=2,a=1} map to the same series.
func canonicalKey(tenant, metric string, labels LabelSet) string {
	var sb strings.Builder
	sb.WriteString(tenant)
	sb.WriteByte(0)
	sb.WriteString(metric)
	sb.WriteByte(0)
	writeSortedLabels(&sb, labels)
	return sb.String()
}

func writeSortedLabels(sb *strings.Builder, labels LabelSet) {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte(0)
		sb.WriteString(labels[k])
		sb.WriteByte(0)
	}
}

// overflowKey is the canonical identity of the synthetic overflow bucket for
// one (tenant, metric). It uses OverflowLabel, which ValidateLabels forbids
// on real series, so it cannot collide with a genuine combination.
func overflowKey(tenant, metric string) string {
	var sb strings.Builder
	sb.WriteString(tenant)
	sb.WriteByte(0)
	sb.WriteString(metric)
	sb.WriteByte(0)
	sb.WriteString(OverflowLabel)
	sb.WriteByte(0)
	sb.WriteString("true")
	sb.WriteByte(0)
	return sb.String()
}

func copyLabels(labels LabelSet) LabelSet {
	out := make(LabelSet, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func mkey(tenant, metric string) string {
	return tenant + "\x00" + metric
}

func splitMKey(k string) (string, string) {
	if i := strings.IndexByte(k, 0); i >= 0 {
		return k[:i], k[i+1:]
	}
	return k, ""
}
