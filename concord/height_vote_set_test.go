package concord

import (
	"fmt"
	"os"
	"testing"

	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto/tmhash"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmtime "github.com/tendermint/tendermint/types/time"

	"github.com/Wondertan/iotwal/concord/pb"
)

var config *cfg.Config // NOTE: must be reset for each _test.go file

func TestMain(m *testing.M) {
	config = cfg.ResetTestRoot("consensus_height_vote_set_test")
	code := m.Run()
	os.RemoveAll(config.RootDir)
	os.Exit(code)
}

func TestPeerCatchupRounds(t *testing.T) {
	valSet, privVals := RandValidatorSet(10, 1)

	hvs := NewHeightVoteSet(config.ChainID(), valSet)

	vote999_0 := makeVoteHR(t, 1, 0, 999, privVals)
	added, err := hvs.AddVote(vote999_0)
	if !added || err != nil {
		t.Error("Expected to successfully add vote from peer", added, err)
	}

	vote1000_0 := makeVoteHR(t, 1, 0, 1000, privVals)
	added, err = hvs.AddVote(vote1000_0)
	if !added || err != nil {
		t.Error("Expected to successfully add vote from peer", added, err)
	}

	vote1001_0 := makeVoteHR(t, 1, 0, 1001, privVals)
	added, err = hvs.AddVote(vote1001_0)
	if err != ErrGotVoteFromUnwantedRound {
		t.Errorf("expected GotVoteFromUnwantedRoundError, but got %v", err)
	}
	if added {
		t.Error("Expected to *not* add vote from peer, too many catchup rounds.")
	}

	added, err = hvs.AddVote(vote1001_0)
	if !added || err != nil {
		t.Error("Expected to successfully add vote from another peer")
	}

}

func makeVoteHR(t *testing.T, height int64, valIndex, round int32, privVals []PrivProposer) *Vote {
	privVal := privVals[valIndex]
	pubKey, err := privVal.GetPubKey()
	if err != nil {
		panic(err)
	}

	randBytes := tmrand.Bytes(tmhash.Size)

	vote := &Vote{
		ValidatorAddress: pubKey.Address(),
		ValidatorIndex:   valIndex,
		Timestamp:        tmtime.Now(),
		Type:             pb.PrecommitType,
		BlockID:          BlockID{Hash: randBytes},
	}
	chainID := config.ChainID()

	v := vote.ToProto()
	err = privVal.SignVote(chainID, v)
	if err != nil {
		panic(fmt.Sprintf("Error signing vote: %v", err))
	}

	vote.Signature = v.Signature

	return vote
}
