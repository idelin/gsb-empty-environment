package netsec

import (
	"crypto/rand"
	"sync"
)

// MICCallback computes the 4-byte MIC over canonical message bytes using
// the device's root key. The same callback authenticates JoinRequests
// (over canonical bytes) and JoinAccepts (over the ciphertext).
type MICCallback func(canonical []byte) ([4]byte, error)

// DeviceInfo is the immutable registry record for one device: fixed
// DevEUI, JoinEUI and root key handle.
type DeviceInfo struct {
	DevEUI  [8]byte
	JoinEUI [8]byte
	RootKey KeyHandle
	MIC     MICCallback
}

// NewDeviceInfo creates a registry record with a freshly generated root
// key imported into the key store.
func NewDeviceInfo(ks *KeyStore, devEUI, joinEUI [8]byte) (*DeviceInfo, error) {
	key := make([]byte, rootKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	h, err := ks.ImportRootKey(key)
	if err != nil {
		return nil, err
	}
	return &DeviceInfo{
		DevEUI:  devEUI,
		JoinEUI: joinEUI,
		RootKey: h,
		MIC:     ks.MICCallback(h),
	}, nil
}

// Registry is the server's fixed device registry.
type Registry struct {
	mu      sync.RWMutex
	devices map[[8]byte]*DeviceInfo
}

func NewRegistry() *Registry {
	return &Registry{devices: make(map[[8]byte]*DeviceInfo)}
}

// Register adds a device; DevEUI values are fixed and may not be
// registered twice.
func (r *Registry) Register(info *DeviceInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[info.DevEUI]; ok {
		return ErrDeviceAlreadyRegistered
	}
	r.devices[info.DevEUI] = info
	return nil
}

func (r *Registry) Lookup(devEUI [8]byte) (*DeviceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[devEUI]
	return d, ok
}
