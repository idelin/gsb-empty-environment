package evidence

import (
	"crypto/ed25519"
	"crypto/sha256"
)

// 域分离标签：不同用途的哈希前缀不同，杜绝跨结构碰撞。
const (
	tagWitness     = "evidence/witness/v1"
	tagRoot        = "evidence/root/v1"
	tagEnvelope    = "evidence/envelope/v1"
	tagRegistry    = "evidence/registry/v1"
	tagBoundary    = "evidence/boundary/v1"
	tagMerkleLeaf  = "evidence/merkle/leaf/v1"
	tagMerklePair  = "evidence/merkle/pair/v1"
	tagMerkleEmpty = "evidence/merkle/empty/v1"
)

// taggedHash 在 SHA-256 中加入域分离标签。
func taggedHash(tag string, payload []byte) Digest {
	h := sha256.New()
	h.Write([]byte(tag))
	h.Write([]byte{0})
	h.Write(payload)
	return Digest(h.Sum(nil))
}

// witnessMessage 是见证签名被约束的消息：节点的完整环境身份、域、构建器
// 与公钥。环境身份在消息内，跨环境同摘要对象的签名不可互相顶替。
type witnessMessage struct {
	Tag     string    `json:"tag"`
	Env     EnvID     `json:"env"`
	Payload Digest    `json:"payload"`
	Domain  DomainID  `json:"domain"`
	Builder BuilderID `json:"builder"`
	PubKey  []byte    `json:"pubkey"`
}

// WitnessMessage 返回某条见证应签名的规范字节。
func WitnessMessage(w Witness) ([]byte, error) {
	return canonicalJSON(witnessMessage{
		Tag:     tagWitness,
		Env:     w.Node.Env,
		Payload: w.Node.Payload,
		Domain:  w.Domain,
		Builder: w.Builder,
		PubKey:  w.PubKey,
	})
}

// SignWitness 用构建器私钥产生见证签名。
func SignWitness(priv ed25519.PrivateKey, env EnvID, payload Digest, domain DomainID, builder BuilderID, pub ed25519.PublicKey) (Witness, error) {
	w := Witness{Domain: domain, Builder: builder, Node: NodeID{Env: env, Payload: payload}, PubKey: append([]byte(nil), pub...)}
	msg, err := WitnessMessage(w)
	if err != nil {
		return Witness{}, err
	}
	w.Sig = ed25519.Sign(priv, msg)
	return w, nil
}

// VerifyWitness 校验见证签名，并强制见证声明的节点身份与签名消息一致。
func VerifyWitness(w Witness) error {
	if len(w.PubKey) != ed25519.PublicKeySize || len(w.Sig) != ed25519.SignatureSize {
		return ErrWitnessSignature
	}
	msg, err := WitnessMessage(w)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(w.PubKey), msg, w.Sig) {
		return ErrWitnessSignature
	}
	return nil
}

// SignRoot 用根权威私钥对根主体签名。
func SignRoot(body RootBody, priv ed25519.PrivateKey) (SignedRoot, error) {
	raw, err := canonicalJSON(body)
	if err != nil {
		return SignedRoot{}, err
	}
	msg := taggedHash(tagRoot, raw)
	return SignedRoot{Body: body, Sig: ed25519.Sign(priv, msg[:])}, nil
}

// VerifyRoot 用根权威公钥校验受签根。
func VerifyRoot(sr SignedRoot, authority ed25519.PublicKey) error {
	raw, err := canonicalJSON(sr.Body)
	if err != nil {
		return err
	}
	msg := taggedHash(tagRoot, raw)
	if len(authority) != ed25519.PublicKeySize || len(sr.Sig) != ed25519.SignatureSize {
		return ErrRootSignature
	}
	if !ed25519.Verify(authority, msg[:], sr.Sig) {
		return ErrRootSignature
	}
	return nil
}

// merkleRoot 对已排序的袋摘要向量构造 Merkle 根。
// 叶子与内部节点使用不同域分离标签；奇数节点直接提升。
func merkleRoot(entries []BagHashEntry) Digest {
	if len(entries) == 0 {
		return taggedHash(tagMerkleEmpty, nil)
	}
	layer := make([]Digest, len(entries))
	for i, e := range entries {
		layer[i] = taggedHash(tagMerkleLeaf, e.Hash[:])
	}
	for len(layer) > 1 {
		next := make([]Digest, 0, (len(layer)+1)/2)
		for i := 0; i < len(layer); i += 2 {
			if i+1 == len(layer) {
				next = append(next, layer[i]) // 奇数节点提升
				continue
			}
			buf := make([]byte, 0, 2*sha256.Size)
			buf = append(buf, layer[i][:]...)
			buf = append(buf, layer[i+1][:]...)
			next = append(next, taggedHash(tagMerklePair, buf))
		}
		layer = next
	}
	return layer[0]
}

// nodeKey 是 map 使用的可比较节点键。
type nodeKey struct {
	env     EnvID
	payload Digest
}

func keyOf(n NodeID) nodeKey { return nodeKey{env: n.Env, payload: n.Payload} }

func idOf(k nodeKey) NodeID { return NodeID{Env: k.env, Payload: k.payload} }
