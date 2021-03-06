// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package dpos

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// API is a user facing RPC API to allow controlling the signer and voting
// mechanisms of the DPOS scheme.
type API struct {
	chain  consensus.ChainHeaderReader
	dpos *Dpos
}

// GetSnapshot retrieves the state snapshot at a given block.
func (api *API) GetSnapshot(number *rpc.BlockNumber) (*Snapshot, error) {
	fmt.Println("")
	// Retrieve the requested block number (or current if none requested)
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	// Ensure we have an actually valid block and return its snapshot
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.dpos.snapshot(api.chain, header.Number.Uint64(), header.Hash(), nil)
}

// GetSnapshotAtHash retrieves the state snapshot at a given block.
func (api *API) GetSnapshotAtHash(hash common.Hash) (*Snapshot, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.dpos.snapshot(api.chain, header.Number.Uint64(), header.Hash(), nil)
}

func (api *API) Test(hash common.Hash) *types.Header {
	return api.chain.GetHeaderByHash(hash)	
}

// Proposals returns the current proposals the node tries to uphold and vote on.
func (api *API) Proposals() map[common.Hash]bool {
	api.dpos.lock.RLock()
	defer api.dpos.lock.RUnlock()

	proposals := make(map[common.Hash]bool)
	for proposalBytes, yesNo := range api.dpos.myProposals {
		proposals[proposalBytes] = yesNo
	}
	return proposals
}

func (api *API) Propose(proposalBytes common.Hash, yesNo bool) error {
	api.dpos.lock.Lock()
	defer api.dpos.lock.Unlock()
	
	proposal := &Proposal{}
	if err := proposal.fromBytes(proposalBytes); err != nil {
		return err
	}
	
	api.dpos.myProposals[proposalBytes] = yesNo
	
	return nil
	
}

// Discard drops a currently running proposal, stopping the signer from casting
// further votes (either for or against).
func (api *API) Discard(proposalBytes common.Hash) {
	api.dpos.lock.Lock()
	defer api.dpos.lock.Unlock()

	delete(api.dpos.myProposals, proposalBytes)
}

