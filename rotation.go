package netsec

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrEmptyBatch 表示换代批次中没有任何事件。
	ErrEmptyBatch = errors.New("netsec: empty rotation batch")
	// ErrDuplicateEventKey 表示批次内两个事件声称拥有相同的旧事件键。
	ErrDuplicateEventKey = errors.New("netsec: duplicate event key in rotation batch")
	// ErrKeyMismatch 表示事件提供的旧事件键与按旧代次重新派生的结果不一致，
	// 换代无法确认事件归属，必须失败关闭而不是重写错误数据。
	ErrKeyMismatch = errors.New("netsec: event key does not match supplied secret")
	// ErrDanglingKey 表示引用指向的旧令牌在批次和已发布映射中都无法解析。
	ErrDanglingKey = errors.New("netsec: unresolvable reference")
	// ErrNonInjectiveMapping 表示两个不同旧令牌会映射到同一新令牌，
	// 这会破坏关联标识的一一对应关系。
	ErrNonInjectiveMapping = errors.New("netsec: rotation mapping is not injective")
)

// Reference 是事件内对另一个伪名（如操作者、父事件）的引用。
type Reference struct {
	// Field 是引用的字段名/角色（例如 actor、parent_event），用于宿主侧排查。
	Field string
	// Token 是旧代次伪名令牌字符串。
	Token string
}

// Event 是参与换代批次的单个安全事件。
//
// Secret 仅在换代过程中存活于内存，用于在新旧两个代次下重新派生令牌，
// 既校验旧事件键归属，也生成新事件键；它不会被写入映射表。
type Event struct {
	// TenantID 是租户域。
	TenantID string
	// FieldType 是事件键字段类型（例如 event_id、email）。
	FieldType string
	// Secret 是事件键的原始秘密。
	Secret []byte
	// OldKey 是当前（旧代次）事件键令牌字符串。
	OldKey string
	// References 是事件内需要一并重写的引用。
	References []Reference
}

// RewrittenReference 是重写后的引用；无法解析时 Missing 为 true 且 Token 保留原值。
type RewrittenReference struct {
	Field   string
	Token   string
	Missing bool
}

// RotatedEvent 是换代输出：新事件键与全部重写后的引用。
type RotatedEvent struct {
	NewKey     string
	References []RewrittenReference
}

// RotationReport 汇总一次批次换代结果。
type RotationReport struct {
	FromGeneration uint32
	ToGeneration   uint32
	// Mapping 是本次新发布的旧令牌 -> 新令牌映射（仅令牌，无原始秘密）。
	Mapping map[string]string
	// Events 与输入批次按索引对齐。
	Events []RotatedEvent
	// Missing 收集所有无法解析的引用，作为显式缺失供宿主审计。
	Missing []RewrittenReference
}

// RotateBatch 在两个密钥代次之间原子地重写整批事件键与引用。
//
// 流程：
//  1. 第一遍：用旧代次校验每个事件键，并用新代次派生新键，建立旧 -> 新映射；
//  2. 第二遍：解析全部引用。批次内前向/循环引用因映射已完整而可解析，
//     批次外引用回退到已发布映射，仍无法解析则保留为显式缺失；
//  3. 整批验证：映射一一对应、新键可重新解析、缺失被显式登记；
//  4. 通过单次 MappingStore.Commit 原子发布。提交失败时不返回任何重写结果，
//     已发布映射保持不变，宿主可用同一批次安全重试。
func (p *Pseudonymizer) RotateBatch(ctx context.Context, fromGeneration, toGeneration uint32, store MappingStore, events []Event) (*RotationReport, error) {
	if len(events) == 0 {
		return nil, ErrEmptyBatch
	}
	if fromGeneration == toGeneration {
		return nil, errors.New("netsec: rotation requires distinct key generations")
	}

	batchMapping := make(map[string]string, len(events))
	newTokens := make([]Token, len(events))

	// 第一遍：校验旧键、派生新键，构建完整的批次内旧 -> 新映射。
	for index := range events {
		event := &events[index]

		oldToken, err := ParseToken(event.OldKey)
		if err != nil {
			return nil, fmt.Errorf("netsec: event %d old key: %w", index, err)
		}
		if oldToken.Generation != fromGeneration {
			return nil, fmt.Errorf("%w: event %d declares generation %d, want %d",
				ErrKeyMismatch, index, oldToken.Generation, fromGeneration)
		}
		ok, err := p.Verify(oldToken, event.TenantID, event.FieldType, event.Secret)
		if err != nil {
			return nil, fmt.Errorf("netsec: event %d verify old key: %w", index, err)
		}
		if !ok {
			return nil, fmt.Errorf("%w: event %d", ErrKeyMismatch, index)
		}

		newToken, err := p.Pseudonymize(toGeneration, event.TenantID, event.FieldType, event.Secret)
		if err != nil {
			return nil, fmt.Errorf("netsec: event %d derive new key: %w", index, err)
		}
		newTokens[index] = newToken

		if existing, ok := batchMapping[event.OldKey]; ok && existing != newToken.String() {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateEventKey, event.OldKey)
		}
		batchMapping[event.OldKey] = newToken.String()
	}

	// 载入已发布映射，作为批次外引用的回退解析来源。
	published := make(map[string]string)
	if err := store.View(ctx, func(snapshot map[string]string) error {
		for oldToken, newToken := range snapshot {
			published[oldToken] = newToken
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("netsec: load published mapping: %w", err)
	}

	// 第二遍：映射已完整，循环引用与前向引用均可解析；无法解析者显式标记缺失。
	report := &RotationReport{
		FromGeneration: fromGeneration,
		ToGeneration:   toGeneration,
		Mapping:        batchMapping,
		Events:         make([]RotatedEvent, len(events)),
	}
	for index := range events {
		event := &events[index]
		rotated := RotatedEvent{NewKey: newTokens[index].String()}
		for _, ref := range event.References {
			resolved, ok := batchMapping[ref.Token]
			if !ok {
				resolved, ok = published[ref.Token]
			}
			rewritten := RewrittenReference{Field: ref.Field, Token: ref.Token}
			if !ok {
				rewritten.Missing = true
				report.Missing = append(report.Missing, RewrittenReference{
					Field:   ref.Field,
					Token:   ref.Token,
					Missing: true,
				})
			} else {
				rewritten.Token = resolved
			}
			rotated.References = append(rotated.References, rewritten)
		}
		report.Events[index] = rotated
	}

	if err := p.verifyBatch(batchMapping, report); err != nil {
		return nil, err
	}

	// 原子发布：失败时整批结果对调用方不可见，已发布存储保持原状。
	if err := store.Commit(ctx, batchMapping); err != nil {
		return nil, fmt.Errorf("netsec: publish rotation mapping: %w", err)
	}
	return report, nil
}

// verifyBatch 在发布前执行整批不变量检查。
func (p *Pseudonymizer) verifyBatch(batchMapping map[string]string, report *RotationReport) error {
	seenNew := make(map[string]string, len(batchMapping))
	for oldToken, newToken := range batchMapping {
		if _, err := ParseToken(oldToken); err != nil {
			return fmt.Errorf("netsec: invalid mapping key: %w", err)
		}
		parsedNew, err := ParseToken(newToken)
		if err != nil {
			return fmt.Errorf("netsec: invalid mapping value: %w", err)
		}
		if parsedNew.Generation != report.ToGeneration {
			return fmt.Errorf("%w: %s carries generation %d", ErrNonInjectiveMapping, newToken, parsedNew.Generation)
		}
		if collisionOwner, ok := seenNew[newToken]; ok {
			return fmt.Errorf("%w: %s and %s collapse to %s",
				ErrNonInjectiveMapping, collisionOwner, oldToken, newToken)
		}
		seenNew[newToken] = oldToken
	}

	// 每个非缺失引用都必须能在本次发布映射中解析到新代次令牌。
	for _, rotated := range report.Events {
		for _, ref := range rotated.References {
			if ref.Missing {
				continue
			}
			parsed, err := ParseToken(ref.Token)
			if err != nil {
				return fmt.Errorf("netsec: rewritten reference invalid: %w", err)
			}
			if parsed.Generation != report.ToGeneration {
				return fmt.Errorf("%w: reference %s not rewritten to new generation", ErrDanglingKey, ref.Token)
			}
		}
	}
	return nil
}
