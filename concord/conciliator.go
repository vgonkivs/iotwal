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
	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

type ProposerStore interface {
	Get(context.Context, string) (*ProposerSet, error)
}

type Validator func(context.Context, []byte) (tmbytes.HexBytes, error)

type conciliator struct {
	pubsub *pubsub.PubSub

	propStore ProposerStore
	propSelf  PrivProposer
}

type concord struct {
	id    string
	topic *pubsub.Topic

	roundMu sync.Mutex
	round   *round // FIXME: Race between AgreeOn and reads in handle

	validate Validator
	propStore ProposerStore
	self      PrivProposer
	selfPK    crypto.PubKey
}

func (c *conciliator) newConcord(id string, pv Validator) (*concord, error) {
	// TODO: There should be at least one subscription
	tpc, err := c.pubsub.Join(id)
	if err != nil {
		return nil, err
	}

	pk, err := c.propSelf.GetPubKey()
	if err != nil {
		return nil, err
	}

	cord := &concord{
		id:       id,
		topic:    tpc,
		validate: pv,
		propStore: c.propStore,
		self: c.propSelf,
		selfPK: pk,
	}
	return cord, c.pubsub.RegisterTopicValidator(id, cord.incoming)
}

func (c *concord) AgreeOn(ctx context.Context, prop []byte) ([]byte, error) {
	// get a fresh proposer set
	// we have to get fresh as they can change after each agreement
	// TODO: Consider passing ProposerSet as a param
	propSet, err := c.propStore.Get(ctx, c.id)
	if err != nil {
		return nil, err
	}

	c.roundMu.Lock()
	defer c.roundMu.Unlock()
	c.round = newRound(c.id, c.topic, &propInfo{propSet, c.self, c.selfPK})

	// TODO: Vote Nil impl
	for ;;c.round.round++ {
		prop, err := c.round.Propose(ctx, prop)
		if err != nil {
			return nil, err
		}

		hash, err := c.validate(ctx, prop)
		if err != nil {
			return nil, err
		}

		err = c.round.Vote(ctx, hash, pb.PrevoteType)
		if err != nil {
			return nil, err
		}
		// TODO: Do we need to wait for all the votes or can we send PreCommits right after?
		err = c.round.Vote(ctx, hash, pb.PrecommitType)
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
