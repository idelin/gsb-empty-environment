package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// memStore 模拟可靠存储：SaveUpgraded 返回 nil 即视为已落盘。
type memStore struct {
	mu       sync.Mutex
	upgraded map[string]bool
	saves    int
	failSave error // 非空时模拟存储故障
}

func newMemStore() *memStore { return &memStore{upgraded: make(map[string]bool)} }

func (s *memStore) IsUpgraded(deviceID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upgraded[deviceID], nil
}

func (s *memStore) SaveUpgraded(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave != nil {
		return s.failSave
	}
	s.saves++
	s.upgraded[deviceID] = true
	return nil
}

func (s *memStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

// 两种格式的正文共享前缀 "AUTH|<device>|"，仅版本字节区分解释。
// 新版正文额外携带 1 字节校验和（前缀字节和 mod 256）。

func legacyBody(deviceID, payload string) []byte {
	return []byte("AUTH|" + deviceID + "|" + payload)
}

func currentBody(deviceID, payload string) []byte {
	base := []byte("AUTH|" + deviceID + "|" + payload)
	var sum byte
	for _, b := range base {
		sum += b
	}
	return append(base, sum)
}

func buildMessage(version byte, body []byte) []byte {
	raw := make([]byte, HeaderLen, HeaderLen+len(body))
	copy(raw[0:4], "NSA1")
	raw[4] = version
	raw[6] = byte(len(body) >> 8)
	raw[7] = byte(len(body))
	return append(raw, body...)
}

func legacyDecoder(body []byte) (Message, error) {
	parts := strings.Split(string(body), "|")
	if len(parts) != 3 || parts[0] != "AUTH" || parts[1] == "" {
		return Message{}, errors.New("legacy: malformed body")
	}
	return Message{DeviceID: parts[1], Payload: []byte(parts[2])}, nil
}

func currentDecoder(body []byte) (Message, error) {
	if len(body) < 2 {
		return Message{}, errors.New("current: body too short")
	}
	base, want := body[:len(body)-1], body[len(body)-1]
	var sum byte
	for _, b := range base {
		sum += b
	}
	if sum != want {
		return Message{}, errors.New("current: checksum mismatch")
	}
	parts := strings.Split(string(base), "|")
	if len(parts) != 3 || parts[0] != "AUTH" || parts[1] == "" {
		return Message{}, errors.New("current: malformed body")
	}
	return Message{DeviceID: parts[1], Payload: []byte(parts[2])}, nil
}

func newTestDispatcher(store Store) *Dispatcher {
	d := NewDispatcher(store)
	d.RegisterDecoder(VersionLegacy, legacyDecoder)
	d.RegisterDecoder(VersionCurrent, currentDecoder)
	return d
}

func TestDispatchBothVersionsBeforeUpgrade(t *testing.T) {
	d := newTestDispatcher(newMemStore())
	for _, version := range []byte{VersionLegacy, VersionCurrent} {
		var body []byte
		if version == VersionLegacy {
			body = legacyBody("dev-1", "ping")
		} else {
			body = currentBody("dev-1", "ping")
		}
		msg, err := d.Dispatch("dev-1", buildMessage(version, body))
		if err != nil {
			t.Fatalf("version %d: %v", version, err)
		}
		if msg.DeviceID != "dev-1" || string(msg.Payload) != "ping" || msg.Version != version {
			t.Fatalf("version %d: unexpected message %+v", version, msg)
		}
	}
}

// 共享首部前缀样本：新旧消息仅版本字节不同；故意损坏的新格式
// 消息必须失败，且绝不回退到旧格式解释。
func TestCorruptedCurrentMessageNeverFallsBack(t *testing.T) {
	d := newTestDispatcher(newMemStore())

	legacyCalls := 0
	d.RegisterDecoder(VersionLegacy, func(body []byte) (Message, error) {
		legacyCalls++
		return legacyDecoder(body)
	})

	good := buildMessage(VersionCurrent, currentBody("dev-2", "secret"))
	bad := make([]byte, len(good))
	copy(bad, good)
	bad[len(bad)-1] ^= 0xFF // 损坏校验和；正文前缀仍是合法的共享前缀

	if _, err := d.Dispatch("dev-2", bad); err == nil {
		t.Fatal("corrupted current-format message must fail")
	}
	if legacyCalls != 0 {
		t.Fatalf("legacy decoder invoked %d times on corrupted current message", legacyCalls)
	}

	// 对照：未损坏的同一消息可以成功。
	if _, err := d.Dispatch("dev-2", good); err != nil {
		t.Fatalf("intact current message rejected: %v", err)
	}
}

// 身份失败同样不得尝试另一解释。
func TestIdentityMismatchNeverFallsBack(t *testing.T) {
	d := newTestDispatcher(newMemStore())
	raw := buildMessage(VersionCurrent, currentBody("dev-real", "ping"))
	if _, err := d.Dispatch("dev-claimed", raw); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("want ErrIdentityMismatch, got %v", err)
	}
}

func TestUpgradedDeviceRejectsLegacy(t *testing.T) {
	store := newMemStore()
	d := newTestDispatcher(store)
	if err := d.Upgrade("dev-3"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Dispatch("dev-3", buildMessage(VersionLegacy, legacyBody("dev-3", "ping")))
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("want ErrDowngrade, got %v", err)
	}
	// 新版仍然可用。
	if _, err := d.Dispatch("dev-3", buildMessage(VersionCurrent, currentBody("dev-3", "ping"))); err != nil {
		t.Fatalf("current message after upgrade: %v", err)
	}
}

// 存储故障：升级不得确认，设备仍按未升级处理。
func TestStorageFailureBlocksUpgradeConfirmation(t *testing.T) {
	store := newMemStore()
	store.failSave = errors.New("disk full")
	d := newTestDispatcher(store)

	if err := d.Upgrade("dev-4"); err == nil {
		t.Fatal("upgrade must not be confirmed when save fails")
	}
	if up, _ := store.IsUpgraded("dev-4"); up {
		t.Fatal("store must not record failed upgrade")
	}
	// 升级未生效，旧版消息仍被接受（尚未登记升级）。
	if _, err := d.Dispatch("dev-4", buildMessage(VersionLegacy, legacyBody("dev-4", "ping"))); err != nil {
		t.Fatalf("legacy message after failed upgrade: %v", err)
	}

	// 存储恢复后升级可重试并成功。
	store.failSave = nil
	if err := d.Upgrade("dev-4"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Dispatch("dev-4", buildMessage(VersionLegacy, legacyBody("dev-4", "ping"))); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("want ErrDowngrade after successful upgrade, got %v", err)
	}
}

// 并发升级：多个 goroutine 同时升级与分派，最终状态必须一致。
func TestConcurrentUpgrades(t *testing.T) {
	store := newMemStore()
	d := newTestDispatcher(store)

	const devices = 16
	var wg sync.WaitGroup
	for i := 0; i < devices; i++ {
		id := fmt.Sprintf("dev-c%d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := d.Upgrade(id); err != nil {
				t.Errorf("upgrade %s: %v", id, err)
			}
		}()
		go func() {
			defer wg.Done()
			// 与升级并发的新版分派必须始终可用。
			if _, err := d.Dispatch(id, buildMessage(VersionCurrent, currentBody(id, "x"))); err != nil {
				t.Errorf("dispatch %s: %v", id, err)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < devices; i++ {
		id := fmt.Sprintf("dev-c%d", i)
		if _, err := d.Dispatch(id, buildMessage(VersionLegacy, legacyBody(id, "x"))); !errors.Is(err, ErrDowngrade) {
			t.Errorf("%s: want ErrDowngrade, got %v", id, err)
		}
	}
	if got := store.saveCount(); got != devices {
		t.Fatalf("store saves = %d, want %d (idempotent upgrade)", got, devices)
	}
}

// 重启：新 Dispatcher 实例（内存缓存为空）从同一 Store 恢复升级
// 状态，旧版许可不得复活。
func TestRestartCannotRestoreLegacyPermission(t *testing.T) {
	store := newMemStore()
	first := newTestDispatcher(store)
	if err := first.Upgrade("dev-5"); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：全新 Dispatcher，内存状态丢失，仅 Store 保留。
	second := newTestDispatcher(store)
	_, err := second.Dispatch("dev-5", buildMessage(VersionLegacy, legacyBody("dev-5", "ping")))
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("after restart: want ErrDowngrade, got %v", err)
	}
	if _, err := second.Dispatch("dev-5", buildMessage(VersionCurrent, currentBody("dev-5", "ping"))); err != nil {
		t.Fatalf("after restart: current message: %v", err)
	}
}

func TestHeaderParsing(t *testing.T) {
	if _, err := ParseHeader([]byte("NSA")); !errors.Is(err, ErrTooShort) {
		t.Fatalf("short header: %v", err)
	}
	raw := buildMessage(VersionCurrent, currentBody("dev-6", "p"))
	raw[3] = 'X'
	if _, err := ParseHeader(raw); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("bad magic: %v", err)
	}
	raw = buildMessage(VersionCurrent, currentBody("dev-6", "p"))
	raw[5] = 1
	if _, err := ParseHeader(raw); !errors.Is(err, ErrBadReserved) {
		t.Fatalf("bad reserved: %v", err)
	}

	d := newTestDispatcher(newMemStore())
	truncated := buildMessage(VersionCurrent, currentBody("dev-6", "payload"))
	if _, err := d.Dispatch("dev-6", truncated[:len(truncated)-2]); !errors.Is(err, ErrTruncatedBody) {
		t.Fatalf("truncated body: %v", err)
	}
	unknown := buildMessage(9, []byte("x"))
	if _, err := d.Dispatch("dev-6", unknown); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("unknown version: %v", err)
	}
}
