package netsec

import "sync"

// LwM2M object identifiers handled by the bootstrap admission package.
const (
	// ObjectSecurity is the LwM2M Security Object ("/0").
	ObjectSecurity = 0
	// ObjectServer is the LwM2M Server Object ("/1").
	ObjectServer = 1
)

// Resource identifiers of the LwM2M Security Object ("/0").
const (
	// ResServerURI is the LwM2M Server URI resource (/0/x/0).
	ResServerURI = 0
	// ResBootstrapServer marks a bootstrap account when true (/0/x/1).
	ResBootstrapServer = 1
	// ResSecurityMode is the security mode resource (/0/x/2).
	ResSecurityMode = 2
	// ResClientIdentity holds the client public key or PSK identity (/0/x/3).
	ResClientIdentity = 3
	// ResServerPublicKey holds the authenticated server key material (/0/x/4).
	ResServerPublicKey = 4
	// ResSecretKey holds the PSK secret or client private key (/0/x/5).
	ResSecretKey = 5
)

// SecurityMode is the enumeration stored in /0/x/2.
type SecurityMode int

const (
	// SecModePSK is Pre-Shared Key mode.
	SecModePSK SecurityMode = 0
	// SecModeRawPK is Raw Public Key mode.
	SecModeRawPK SecurityMode = 1
	// SecModeCertificate is X.509 certificate mode.
	SecModeCertificate SecurityMode = 2
	// SecModeNoSec disables transport security and can never be trusted.
	SecModeNoSec SecurityMode = 3
)

// String returns the LwM2M name of the security mode.
func (m SecurityMode) String() string {
	switch m {
	case SecModePSK:
		return "PSK"
	case SecModeRawPK:
		return "RawPublicKey"
	case SecModeCertificate:
		return "Certificate"
	case SecModeNoSec:
		return "NoSec"
	default:
		return "Unknown"
	}
}

// storeData is a deep-copiable snapshot of the whole object store.
type storeData map[int]map[int]map[int]any

// Store is an in-memory LwM2M object/instance/resource store guarded by a
// mutex. Values are kept as generic scalars or []byte blobs (key material).
type Store struct {
	mu   sync.RWMutex
	data storeData
}

// NewStore returns an empty resource store.
func NewStore() *Store {
	return &Store{data: storeData{}}
}

// SetResource creates or overwrites a single resource value.
func (s *Store) SetResource(obj, inst, res int, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInstance(obj, inst)[res] = cloneValue(value)
}

// GetResource reads a single resource value.
func (s *Store) GetResource(obj, inst, res int) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	instance, ok := s.data[obj][inst]
	if !ok {
		return nil, false
	}
	value, ok := instance[res]
	return cloneValue(value), ok
}

// DeleteInstance removes one object instance. It is a no-op if absent.
func (s *Store) DeleteInstance(obj, inst int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if instances, ok := s.data[obj]; ok {
		delete(instances, inst)
		if len(instances) == 0 {
			delete(s.data, obj)
		}
	}
}

// DeleteObject removes every instance of an object.
func (s *Store) DeleteObject(obj int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, obj)
}

// Instances returns the sorted instance identifiers of an object.
func (s *Store) Instances(obj int) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	instances := s.data[obj]
	ids := make([]int, 0, len(instances))
	for id := range instances {
		ids = append(ids, id)
	}
	sortInts(ids)
	return ids
}

// resourceIDs returns the sorted resource identifiers of one instance.
func (s *Store) resourceIDs(obj, inst int) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	instance, ok := s.data[obj][inst]
	if !ok {
		return nil
	}
	ids := make([]int, 0, len(instance))
	for id := range instance {
		ids = append(ids, id)
	}
	sortInts(ids)
	return ids
}

// Objects returns the sorted object identifiers currently held.
func (s *Store) Objects() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]int, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	sortInts(ids)
	return ids
}

// IsBootstrapAccount reports whether a Security Object instance carries
// Bootstrap-Server (/0/x/1) == true.
func (s *Store) IsBootstrapAccount(inst int) bool {
	value, ok := s.GetResource(ObjectSecurity, inst, ResBootstrapServer)
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	return ok && flag
}

// HasSecureManagementAccount reports whether at least one Security Object
// instance is a management server account (Bootstrap-Server != true) using a
// security mode other than NoSec.
func (s *Store) HasSecureManagementAccount() bool {
	for _, inst := range s.Instances(ObjectSecurity) {
		if s.IsBootstrapAccount(inst) {
			continue
		}
		raw, ok := s.GetResource(ObjectSecurity, inst, ResSecurityMode)
		if !ok {
			continue
		}
		mode, ok := toInt(raw)
		if ok && SecurityMode(mode) != SecModeNoSec {
			return true
		}
	}
	return false
}

// snapshot returns a deep copy of the store for transactional rollback.
func (s *Store) snapshot() storeData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneData(s.data)
}

// restore replaces the whole store content with a previous snapshot.
func (s *Store) restore(snap storeData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = cloneData(snap)
}

func (s *Store) ensureInstance(obj, inst int) map[int]any {
	objects := s.data
	instances, ok := objects[obj]
	if !ok {
		instances = map[int]map[int]any{}
		objects[obj] = instances
	}
	instance, ok := instances[inst]
	if !ok {
		instance = map[int]any{}
		instances[inst] = instance
	}
	return instance
}

// ResourceSet is the resource map of one instance, keyed by resource ID.
type ResourceSet map[int]any

// ObjectWrite is a set of instance resource maps keyed by instance ID.
type ObjectWrite map[int]ResourceSet

func cloneData(in storeData) storeData {
	out := make(storeData, len(in))
	for obj, instances := range in {
		outInstances := make(map[int]map[int]any, len(instances))
		for inst, resources := range instances {
			outResources := make(map[int]any, len(resources))
			for res, value := range resources {
				outResources[res] = cloneValue(value)
			}
			outInstances[inst] = outResources
		}
		out[obj] = outInstances
	}
	return out
}

func cloneValue(value any) any {
	if raw, ok := value.([]byte); ok {
		dup := make([]byte, len(raw))
		copy(dup, raw)
		return dup
	}
	return value
}

func toInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case SecurityMode:
		return int(typed), true
	default:
		return 0, false
	}
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
