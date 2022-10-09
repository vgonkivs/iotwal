package concord

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/Wondertan/iotwal/concord/pb"
	"github.com/celestiaorg/go-libp2p-messenger/serde"
	"github.com/libp2p/go-libp2p-core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

type ProposerStore interface {
	Get(context.Context, string) (*ProposerSet, error)
	Save(context.Context, string, *ProposerSet) error
}

type Validator func(context.Context, []byte) (tmbytes.HexBytes, error)

type conciliator struct {
	pubsub *pubsub.PubSub

	valStore ProposerStore
	valSelf  PrivValidator
}

type concord struct {
	id    string
	topic *pubsub.Topic

	roundMu sync.Mutex
	round   *round // FIXME: Race between AgreeOn and reads in handle

	validate Validator
	valInfo  *propInfo
}

func (c *conciliator) newConcord(ctx context.Context, id string, pv Validator) (*concord, error) {
	tpc, err := c.pubsub.Join(id)
	if err != nil {
		return nil, err
	}

	valSet, err := c.valStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	pk, err := c.valSelf.GetPubKey()
	if err != nil {
		return nil, err
	}

	cord := &concord{
		id:       id,
		topic:    tpc,
		validate: pv,
		valInfo:  &propInfo{valSet, c.valSelf, pk},
	}
	return cord, c.pubsub.RegisterTopicValidator(id, cord.incoming)
}

func (c *concord) AgreeOn(ctx context.Context, prop []byte) ([]byte, error) {
	c.roundMu.Lock()
	defer c.roundMu.Unlock()
	c.round = newRound(c.id, c.topic, c.valInfo)

	for ;;c.round.round++ {
		prop, err := c.round.Propose(ctx, prop)
		if err != nil {
			return nil, err
		}

		hash, err := c.validate(ctx, prop)
		if err != nil {
			return nil, err
		}

		err = c.round.Vote(ctx, hash)
		if err != nil {
			return nil, err
		}
		// TODO: Do we need to wait for all the votes or can we send PreCommits right after?
		err = c.round.PreCommit(ctx, hash)
		if err != nil {
			return nil, err
		}

		return prop, nil
	}
}

func (c *concord) incoming(ctx context.Context, _ peer.ID, pmsg *pubsub.Message) pubsub.ValidationResult {
	err := c.handle(ctx, pmsg)
	if err != nil {
		return pubsub.ValidationReject
	}

	return pubsub.ValidationAccept
}

func (c *concord) handle(ctx context.Context, pmsg *pubsub.Message) error {
	tmsg := &pb.Message{}
	_, err := serde.Unmarshal(tmsg, pmsg.Data)
	if err != nil {
		return err
	}

	msg, err := MsgFromProto(tmsg)
	if err != nil {
		return err
	}

	err = msg.ValidateBasic()
	if err != nil {
		return err
	}

	switch msg := msg.(type) {
	case *ProposalMessage:
		return c.round.rcvProposal(ctx, msg.Proposal)
	case *VoteMessage:
		return c.round.rcvVote(ctx, msg.Vote, peer.ID(pmsg.From))
	default:
		return fmt.Errorf("wrong msg type %v", reflect.TypeOf(msg))
	}
}
