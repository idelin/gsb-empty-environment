package netsec

import (
	"fmt"
	"sort"
)

// Records is the host-supplied ASPA dataset. Each key is a customer ASN and
// each value is the complete set of providers authorized by that customer.
// A present key with an empty provider set is a complete ASPA record which
// authorizes no provider.
type Records map[ASN][]ASN

// Dataset is an immutable ASPA snapshot. Callers may share a Dataset across
// goroutines without copying it.
type Dataset struct {
	version   string
	providers map[ASN]map[ASN]struct{}
}

// NewDataset validates and freezes an ASPA dataset.
func NewDataset(version string, records Records) (*Dataset, error) {
	if version == "" {
		return nil, fmt.Errorf("%w: datasetVersion must not be empty", ErrInvalidDataset)
	}

	frozen := make(map[ASN]map[ASN]struct{}, len(records))
	for customer, providers := range records {
		if err := ValidateASN(customer); err != nil {
			return nil, fmt.Errorf("%w: customer: %w", ErrInvalidDataset, err)
		}
		set := make(map[ASN]struct{}, len(providers))
		for _, provider := range providers {
			if err := ValidateASN(provider); err != nil {
				return nil, fmt.Errorf("%w: provider of AS %d: %w", ErrInvalidDataset, uint32(customer), err)
			}
			if provider == customer {
				return nil, fmt.Errorf("%w: AS %d authorizes itself as a provider", ErrInvalidDataset, uint32(customer))
			}
			set[provider] = struct{}{}
		}
		frozen[customer] = set
	}
	return &Dataset{version: version, providers: frozen}, nil
}

// MustNewDataset is NewDataset with panic-on-error semantics for static
// configuration.
func MustNewDataset(version string, records Records) *Dataset {
	dataset, err := NewDataset(version, records)
	if err != nil {
		panic(err)
	}
	return dataset
}

// Version returns the datasetVersion associated with this immutable snapshot.
func (d *Dataset) Version() string {
	if d == nil {
		return ""
	}
	return d.version
}

// Size returns the number of customer ASPA records in the snapshot.
func (d *Dataset) Size() int {
	if d == nil {
		return 0
	}
	return len(d.providers)
}

// Providers returns a sorted copy of the provider set for customer. The bool
// reports whether a complete ASPA record exists for that customer.
func (d *Dataset) Providers(customer ASN) ([]ASN, bool) {
	if d == nil {
		return nil, false
	}
	set, ok := d.providers[customer]
	if !ok {
		return nil, false
	}
	result := make([]ASN, 0, len(set))
	for provider := range set {
		result = append(result, provider)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, true
}

// HasRecord reports whether the dataset contains a complete ASPA record for
// customer, including an empty record.
func (d *Dataset) HasRecord(customer ASN) bool {
	if d == nil {
		return false
	}
	_, ok := d.providers[customer]
	return ok
}

// Authorizes reports whether customer explicitly authorizes provider as an
// upstream provider. It returns false for an absent record; use HasRecord to
// distinguish an explicit denial from a data gap.
func (d *Dataset) Authorizes(customer, provider ASN) bool {
	if d == nil {
		return false
	}
	set, ok := d.providers[customer]
	if !ok {
		return false
	}
	_, ok = set[provider]
	return ok
}

func (d *Dataset) changedCustomers(other *Dataset) map[ASN]struct{} {
	changed := make(map[ASN]struct{})
	if d == nil && other == nil {
		return changed
	}
	if d == nil {
		for customer := range other.providers {
			changed[customer] = struct{}{}
		}
		return changed
	}
	if other == nil {
		for customer := range d.providers {
			changed[customer] = struct{}{}
		}
		return changed
	}
	for customer, providers := range d.providers {
		otherProviders, ok := other.providers[customer]
		if !ok || !sameProviderSet(providers, otherProviders) {
			changed[customer] = struct{}{}
		}
	}
	for customer := range other.providers {
		if _, ok := d.providers[customer]; !ok {
			changed[customer] = struct{}{}
		}
	}
	return changed
}

func sameProviderSet(a, b map[ASN]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for provider := range a {
		if _, ok := b[provider]; !ok {
			return false
		}
	}
	return true
}
