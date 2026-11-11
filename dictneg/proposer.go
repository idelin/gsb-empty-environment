package dictneg

import (
	"errors"
	"fmt"
)

// ProposerConfig 是发送端的配置。
type ProposerConfig struct {
	// Store 持久化发送端状态与字典内容，必填。
	Store Store
	// Name 是该角色在 Store 中的状态命名空间。
	Name string
}

// Proposer 是字典协商的发送端（压缩数据的生产方）。
//
// 它负责发起三阶段协商：Install -> 等待 Ack -> Enable。任何阶段的消息
// 丢失都可通过 Resync 安全重放；活动摘要只有在接收端确认并启用之后才
// 切换，安装失败则完整保留旧版字典。
type Proposer struct {
	store Store
	name  string
	state ProposerState
}

// NewProposer 从 Store 恢复发送端。若历史状态存在（重连场景），直接沿用
// 已持久确认的活动摘要与在途安装。
func NewProposer(cfg ProposerConfig) (*Proposer, error) {
	if cfg.Store == nil {
		return nil, errors.New("dictneg: nil store")
	}
	name := cfg.Name
	if name == "" {
		name = "default"
	}
	state, err := cfg.Store.LoadProposerState(name)
	if err != nil {
		return nil, fmt.Errorf("dictneg: load proposer state: %w", err)
	}
	if state == nil {
		state = &ProposerState{}
	}
	return &Proposer{store: cfg.Store, name: name, state: *state}, nil
}

// ActiveDigest 返回当前用于编码的字典摘要；零值表示尚无确认字典。
func (p *Proposer) ActiveDigest() Digest {
	return p.state.Active
}

// Pending 返回当前在途安装；零值 PendingInstall 表示没有在途安装。
func (p *Proposer) Pending() PendingInstall {
	return p.state.Pending
}

// BeginInstall 发起一次字典替换。
//
// 字典内容先持久化到 Store，再生成带新 Epoch 的 Install 事件。若已有在途
// 安装会被新的安装取代（旧 Epoch 的迟到确认将被丢弃）。该方法不改变
// 活动摘要：在 Enable 完成前，数据帧继续绑定旧摘要。
func (p *Proposer) BeginInstall(content []byte) (Event, error) {
	digest := SumDigest(content)
	if digest == p.state.Active {
		return Event{}, fmt.Errorf("%w: digest already active", ErrInvalidWire)
	}
	if err := p.store.PutDictionary(digest, content); err != nil {
		return Event{}, err
	}
	p.state.Epoch++
	p.state.Pending = PendingInstall{Digest: digest, Confirmed: false}
	if err := p.save(); err != nil {
		return Event{}, err
	}
	return Event{Kind: EventInstall, Epoch: p.state.Epoch, Digest: digest, Payload: content}, nil
}

// HandleEvent 处理接收端回传的 Ack/Nack。
//
// Ack 有两种含义：匹配在途安装时表示“安装已确认”，匹配当前活动摘要
// 时表示“启用已确认”。两者都在返回前持久化。Nack 或过期 Epoch 都不会
// 改变活动摘要；Nack 会终止本次在途安装并保留旧版字典。
func (p *Proposer) HandleEvent(ev Event) error {
	if ev.Epoch < p.state.Epoch {
		return fmt.Errorf("%w: epoch %d < %d", ErrStaleEvent, ev.Epoch, p.state.Epoch)
	}
	if ev.Epoch > p.state.Epoch {
		return fmt.Errorf("%w: unknown epoch %d", ErrInvalidWire, ev.Epoch)
	}
	switch ev.Kind {
	case EventAck:
		switch {
		case !p.state.Pending.IsZero() && ev.Digest == p.state.Pending.Digest:
			if p.state.Pending.Confirmed {
				return nil // 重复 Ack：幂等忽略
			}
			p.state.Pending.Confirmed = true
			return p.save()
		case !p.state.Active.IsZero() && ev.Digest == p.state.Active:
			// 启用事件的确认：接收端已切换到活动字典。
			if p.state.ActiveConfirmed {
				return nil
			}
			p.state.ActiveConfirmed = true
			return p.save()
		default:
			return fmt.Errorf("%w: ack digest %s does not match pending", ErrInvalidWire, ev.Digest)
		}
	case EventNack:
		if !p.state.Pending.IsZero() && ev.Digest == p.state.Pending.Digest {
			p.state.Pending = PendingInstall{}
			if err := p.save(); err != nil {
				return err
			}
		}
		return fmt.Errorf("%w: digest %s", ErrInstallRejected, ev.Digest)
	default:
		return fmt.Errorf("%w: proposer only handles ack/nack, got %s", ErrInvalidWire, ev.Kind)
	}
}

// Enable 在 Ack 已持久化后生成独立的启用事件，并把活动摘要切换到新字典。
//
// 启用状态先落盘再返回事件；接收端会对启用回 Ack，发送端持久化该确认。
// 启用事件或其确认丢失时，重连的 Resync 会基于持久状态重放整条
// 安装+启用链，两端不会错位。
func (p *Proposer) Enable() (Event, error) {
	digest, err := p.transitionEnable()
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: EventEnable, Epoch: p.state.Epoch, Digest: digest}, nil
}

// transitionEnable 把已确认的在途安装切换为活动字典并持久化。
func (p *Proposer) transitionEnable() (Digest, error) {
	if p.state.Pending.IsZero() {
		return Digest{}, errors.New("dictneg: no pending install to enable")
	}
	if !p.state.Pending.Confirmed {
		return Digest{}, errors.New("dictneg: cannot enable before ack")
	}
	digest := p.state.Pending.Digest
	p.state.Active = digest
	p.state.Pending = PendingInstall{}
	p.state.ActiveConfirmed = false
	if err := p.save(); err != nil {
		return Digest{}, err
	}
	return digest, nil
}

// WrapFrame 用当前活动字典的摘要绑定一帧压缩数据。
//
// 本方法不做压缩（压缩由宿主完成），只负责把摘要与序号写进帧头，保证
// 接收端绝无猜测字典的余地。
func (p *Proposer) WrapFrame(seq uint64, compressed []byte) (Frame, error) {
	if p.state.Active.IsZero() {
		return Frame{}, ErrNoActiveDictionary
	}
	return Frame{Digest: p.state.Active, Seq: seq, Payload: compressed}, nil
}

// Resync 返回重连后需要重放给接收端的事件序列，使两端收敛到已持久
// 确认的版本：
//
//   - 在途且已确认、尚未启用：重放 Install 与 Enable；
//   - 在途未确认：重放 Install，等待重新确认；
//   - 已启用但启用确认未持久化：重放活动字典的 Install 与 Enable，
//     接收端幂等去重；
//   - 其余情况：不产生事件，当前活动字典继续有效。
//
// 所有重放都复用原 Epoch，接收端按 Epoch 去重，因此重复重放幂等。
func (p *Proposer) Resync() ([]Event, error) {
	if !p.state.Pending.IsZero() {
		digest := p.state.Pending.Digest
		content, err := p.store.GetDictionary(digest)
		if err != nil {
			return nil, err
		}
		events := []Event{{Kind: EventInstall, Epoch: p.state.Epoch, Digest: digest, Payload: content}}
		if p.state.Pending.Confirmed {
			// 重放启用即代表本端完成启用迁移，与 Enable 语义一致。
			if _, err := p.transitionEnable(); err != nil {
				return nil, err
			}
			events = append(events, Event{Kind: EventEnable, Epoch: p.state.Epoch, Digest: digest})
		}
		return events, nil
	}
	if p.state.Active.IsZero() || p.state.ActiveConfirmed {
		return nil, nil
	}
	// 启用事件或其确认可能已丢失：重放整条链，接收端按 Epoch 幂等去重。
	content, err := p.store.GetDictionary(p.state.Active)
	if err != nil {
		return nil, err
	}
	return []Event{
		{Kind: EventInstall, Epoch: p.state.Epoch, Digest: p.state.Active, Payload: content},
		{Kind: EventEnable, Epoch: p.state.Epoch, Digest: p.state.Active},
	}, nil
}

func (p *Proposer) save() error {
	if err := p.store.SaveProposerState(p.name, &p.state); err != nil {
		return err
	}
	return nil
}
