package netsec

import "errors"

var (
	// ErrUnknownTenant means the tenant was not registered with the IOMMU.
	ErrUnknownTenant = errors.New("netsec: unknown tenant")
	// ErrUnknownDevice means the device was not registered with the IOMMU.
	ErrUnknownDevice = errors.New("netsec: unknown device")
	// ErrDeviceExists means a device ID has already been registered.
	ErrDeviceExists = errors.New("netsec: device already registered")
	// ErrPageOwned means a physical page already belongs to another tenant.
	ErrPageOwned = errors.New("netsec: physical page owned by another tenant")
	// ErrPageUnowned means a mapping refers to a page with no host owner.
	ErrPageUnowned = errors.New("netsec: physical page has no owner")
	// ErrForeignPage means a device tried to map a page owned by another tenant.
	ErrForeignPage = errors.New("netsec: physical page belongs to another tenant")
	// ErrInvalidIOVA means an IOVA is not page aligned or is otherwise invalid.
	ErrInvalidIOVA = errors.New("netsec: invalid IOVA")
	// ErrInvalidLength means a transfer or mapping has an invalid length.
	ErrInvalidLength = errors.New("netsec: invalid length")
	// ErrOverflow means an IOVA, length, or page computation overflowed uint64.
	ErrOverflow = errors.New("netsec: address range overflow")
	// ErrInvalidAccess means a permission mask is zero or contains unknown bits.
	ErrInvalidAccess = errors.New("netsec: invalid access mask")
	// ErrAlreadyMapped means an IOVA page is already mapped.
	ErrAlreadyMapped = errors.New("netsec: IOVA already mapped")
	// ErrNotMapped means a requested IOVA page has no active translation.
	ErrNotMapped = errors.New("netsec: IOVA not mapped")
	// ErrPermission means a transfer requests an operation the mapping disallows.
	ErrPermission = errors.New("netsec: mapping does not permit access")
	// ErrMixedDomain means translations in one request do not share a domain.
	ErrMixedDomain = errors.New("netsec: request mixes IOMMU domains")
	// ErrNilCallback means Transfer was called without an access callback.
	ErrNilCallback = errors.New("netsec: nil access callback")
	// ErrGenerationExhausted means a device generation counter overflowed.
	ErrGenerationExhausted = errors.New("netsec: device generation exhausted")
)

// ErrAccessDenied is an alias for ErrPermission for callers that use that name.
var ErrAccessDenied = ErrPermission
