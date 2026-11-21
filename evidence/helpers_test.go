package evidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"testing"
)

// deterministicKey 从字符串种子派生确定性 Ed25519 密钥对，测试可复现。
func deterministicKey(seedText string) (ed25519.PublicKey, ed25519.PrivateKey) {
	var seed [ed25519.SeedSize]byte
	sum := sha256.Sum256([]byte("test-key-seed:" + seedText))
	copy(seed[:], sum[:])
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return pub, priv
}

func payloadDigest(s string) Digest { return sha256.Sum256([]byte(s)) }

type fixture struct {
	authPub  ed25519.PublicKey
	authPriv ed25519.PrivateKey

	domains  map[DomainID]ed25519.PrivateKey
	builders map[BuilderID]ed25519.PrivateKey
	pubkeys  map[BuilderID]ed25519.PublicKey

	// registry 保持在“当前已发布世代”的状态；pending 收集进入下一世代
	// 的补丁信封，更新时作为输入，之后再 commit。
	registry *RegistryState
	seq      uint64
	pending  []Envelope
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv := deterministicKey("root-authority")
	f := &fixture{
		authPub:  pub,
		authPriv: priv,
		domains:  map[DomainID]ed25519.PrivateKey{},
		builders: map[BuilderID]ed25519.PrivateKey{},
		pubkeys:  map[BuilderID]ed25519.PublicKey{},
		registry: GenesisRegistry(),
	}
	return f
}

func (f *fixture) addDomain(t *testing.T, id DomainID, name string) {
	t.Helper()
	f.applyGov(t, RegistryEvent{Kind: GovAddDomain, DomainID: id, Name: name})
}

func (f *fixture) addBuilder(t *testing.T, id BuilderID, domain DomainID) {
	t.Helper()
	pub, priv := deterministicKey("builder:" + string(id))
	f.builders[id] = priv
	f.pubkeys[id] = pub
	f.applyGov(t, RegistryEvent{Kind: GovAddBuilder, DomainID: domain, Builder: id, PubKey: pub})
}

// stagedGov 在不改变已发布注册表的前提下，对治理补丁做本地预演以得到
// 下一条信封的前像摘要，并缓存预演后的注册表用于连续签名。
func (f *fixture) stagedGov(t *testing.T, ev RegistryEvent) (Envelope, *RegistryState) {
	t.Helper()
	rh, err := f.registry.Hash()
	if err != nil {
		t.Fatalf("registry hash: %v", err)
	}
	f.seq++
	env, err := SignEnvelope(Envelope{Seq: f.seq, Gov: &ev, PrevRegHash: rh}, f.authPriv)
	if err != nil {
		t.Fatalf("sign envelope: %v", err)
	}
	next, err := f.registry.Apply(ev)
	if err != nil {
		t.Fatalf("apply gov: %v", err)
	}
	return env, next
}

// applyGov 发布一条治理补丁：立即推进已发布注册表（用于创世前的建图）。
func (f *fixture) applyGov(t *testing.T, ev RegistryEvent) Envelope {
	env, next := f.stagedGov(t, ev)
	f.registry = next
	return env
}

// stageGov 为下一世代缓存一条治理补丁，保持已发布注册表不变。
func (f *fixture) stageGov(t *testing.T, ev RegistryEvent) Envelope {
	env, next := f.stagedGov(t, ev)
	f.pending = append(f.pending, env)
	f.registry = next // 预演视图推进，但 OldRegistry 由调用方在 stage 前快照
	return env
}

// witness 用构建器私钥为某环境节点签发见证。
func (f *fixture) witness(t *testing.T, builder BuilderID, domain DomainID, id NodeID) Witness {
	t.Helper()
	w, err := SignWitness(f.builders[builder], id.Env, id.Payload, domain, builder, f.pubkeys[builder])
	if err != nil {
		t.Fatalf("sign witness: %v", err)
	}
	return w
}

func (f *fixture) nodeEnvelope(t *testing.T, np NodePatch) Envelope {
	t.Helper()
	rh, _ := f.registry.Hash()
	f.seq++
	env, err := SignEnvelope(Envelope{Seq: f.seq, Node: &np, PrevRegHash: rh}, f.authPriv)
	if err != nil {
		t.Fatalf("sign node envelope: %v", err)
	}
	f.pending = append(f.pending, env)
	return env
}

// snapshotRegistry 返回已发布注册表的独立快照（用于 Update 的 OldRegistry）。
func (f *fixture) snapshotRegistry() *RegistryState { return f.registry.Clone() }

// stagedPatches 返回当前缓存的下一世代补丁。
func (f *fixture) stagedPatches() []Envelope { return append([]Envelope(nil), f.pending...) }

// commit 在一次成功更新后清空暂存区（注册表预演视图已等于新世代）。
func (f *fixture) commit() { f.pending = nil }

// nodeState 构造节点状态并自动附加给定见证。
func nodeState(id NodeID, bag BagID, deps []NodeID, ws ...Witness) NodeState {
	return NodeState{ID: id, Bag: bag, Deps: deps, Witnesses: ws}
}
