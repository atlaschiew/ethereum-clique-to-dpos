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
	"bytes"
	"encoding/json"
	"encoding/binary"
	"sort"
	"time"
	"fmt"
	"math/big"
	"math/rand"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/core/state"
	_ "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	lru "github.com/hashicorp/golang-lru"
	
)

const (
	dbSnapPrefix string = "dpos-"
	
)

/*
每张票的信息，它将影响到Tally.Votes的结果
*/
type Vote struct {
	Signer   common.Address `json:"signer"`   // Authorized signer that cast this vote
	Block    uint64         `json:"block"`    // Block number the vote was cast in (expire old votes)
	YesNo    bool           `json:"yesno"`
	Proposal common.Hash    `json:"proposal"` // Proposal bytes
}

// Snapshot is the state of the authorization voting at a given point in time.
type Snapshot struct {
	config   *params.DposConfig // Consensus engine parameters to fine tune behavior
	sigcache *lru.ARCCache        // 把最近的signature缓存上来，加快ecrecover的处理速度

	Number  uint64                      `json:"number"`   //快照会一直更新区块高度
	Hash    common.Hash                 `json:"hash"`     //快照会一直更新区块哈希
	
	ConfirmedSigners map[common.Address]uint16 `json:"signers"`  //当前合格的签名者， 值为出块数
	UnconfirmedSigners map[common.Address]struct{} `json:"ucsigners"`  //即将成为合格签名者
	
	ConfirmedProposals map[uint8]common.Hash `json:"proposals"`//记录已定案结果
	UnconfirmedProposals map[uint8]common.Hash `json:"ucproposals"`//记录即将定案结果

	Candidates map[common.Address]struct{} `json:"candidates"` //候选人
	Delegators map[common.Address]common.Address `json:"delegators"` //委任人，键值为delegator地址，值为signer地址
	
	Recents map[uint64]common.Address   `json:"recents"`  //Set of recent signers for spam protections
	Votes   []*Vote                     `json:"votes"`    //记录每张投票*Vote
	Tally   map[common.Hash]int         `json:"tally"`    //键值为proposal bytes, 值为获得的votes, 超过半数票提案就通过
	
}

// signersAscending implements the sort interface to allow sorting a list of addresses
type signersAscending []common.Address

func (s signersAscending) Len() int           { return len(s) }
func (s signersAscending) Less(i, j int) bool { return bytes.Compare(s[i][:], s[j][:]) < 0 }
func (s signersAscending) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// newSnapshot creates a new snapshot with the specified startup parameters. This
// method does not initialize the set of recent signers, so only ever use if for
// the genesis block.
func newSnapshot(config *params.DposConfig, sigcache *lru.ARCCache, number uint64, hash common.Hash, signers []common.Address, proposals []*Proposal) *Snapshot {
	
	snap := &Snapshot{
		config:   config,
		sigcache: sigcache,
		Number:   number,
		Hash:     hash,
		
		ConfirmedSigners:  make(map[common.Address]uint16),
		UnconfirmedSigners:  make(map[common.Address]struct{}),
		
		ConfirmedProposals:make(map[uint8]common.Hash),
		UnconfirmedProposals:make(map[uint8]common.Hash),
		
		Candidates:make(map[common.Address]struct{}),
		Delegators:make(map[common.Address]common.Address),
		
		Recents:  make(map[uint64]common.Address),
		Tally:    make(map[common.Hash]int),
	}
	
	for _, signer := range signers {
		snap.ConfirmedSigners[signer] = uint16(0)
		
		if number == uint64(0) {
			snap.Candidates[signer] = struct{}{}
		}
	}
	
	for _, proposal := range proposals {
		
		hash, err:=proposal.toBytes()
		_ = err
		snap.ConfirmedProposals[ proposal.Id ] = hash
	}
	
	return snap
}

// loadSnapshot loads an existing snapshot from the database.
func loadSnapshot(config *params.DposConfig, sigcache *lru.ARCCache, db ethdb.Database, hash common.Hash) (*Snapshot, error) {
	blob, err := db.Get(append([]byte(dbSnapPrefix), hash[:]...))
	if err != nil {
		return nil, err
	}
	
	snap := new(Snapshot)
	if err := json.Unmarshal(blob, snap); err != nil {
		return nil, err
	}
	
	snap.config = config
	snap.sigcache = sigcache

	return snap, nil
}


// store inserts the snapshot into the database.
func (s *Snapshot) store(db ethdb.Database) error {
	blob, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return db.Put(append([]byte(dbSnapPrefix), s.Hash[:]...), blob)
}

// copy creates a deep copy of the snapshot, though not the individual votes.
func (s *Snapshot) copy() *Snapshot {
	
	cpy := &Snapshot{
		config:   s.config,
		sigcache: s.sigcache,
		Number:   s.Number,
		Hash:     s.Hash,
		
		ConfirmedSigners:  make(map[common.Address]uint16),
		UnconfirmedSigners: make(map[common.Address]struct{}),
		ConfirmedProposals:  make(map[uint8]common.Hash),
		UnconfirmedProposals:make(map[uint8]common.Hash),
		
		Candidates: make(map[common.Address]struct{}),
		Delegators: make(map[common.Address]common.Address),
		
		
		Recents:  make(map[uint64]common.Address),
		Votes:    make([]*Vote, len(s.Votes)),
		Tally:    make(map[common.Hash]int),
	}
	
	for signer, mintCnt := range s.ConfirmedSigners {
		cpy.ConfirmedSigners[signer] = mintCnt
	}
	
	for signer := range s.UnconfirmedSigners {
		cpy.UnconfirmedSigners[signer] = struct{}{}
	}
	
	for candidate := range s.Candidates {
		cpy.Candidates[candidate] = struct{}{}
	}
	
	for delegator, signer := range s.Delegators {
		cpy.Delegators[delegator] = signer
	}
	
	for proposalId, proposalBytes := range s.ConfirmedProposals {
		cpy.ConfirmedProposals[ proposalId ] = proposalBytes
	}
	
	for proposalId, proposalBytes := range s.UnconfirmedProposals {
		cpy.UnconfirmedProposals[ proposalId ] = proposalBytes
	}
	
	for block, signer := range s.Recents {
		cpy.Recents[block] = signer
	}
	
	for proposalBytes, votes := range s.Tally {
		cpy.Tally[proposalBytes] = votes
	}
	copy(cpy.Votes, s.Votes)

	return cpy
}


//取signer的最后一张票
func (s *Snapshot) lastVote(signer common.Address, proposalBytes common.Hash) *Vote {
	
	var lastVote *Vote
	
	proposal := &Proposal{}
	if err :=proposal.fromBytes(proposalBytes);err == nil {
		for _, vote := range s.Votes {
			if vote.Signer == signer && vote.Proposal == proposalBytes {
				lastVote = vote
			}
		}
	}
	
	return lastVote
}

/*
如果之前有票，根据最后一张票，决定这次能投的必定是反方向的票
如果之前空票，那么第一张必须是赞成票
*/
func (s *Snapshot) validVote(signer common.Address, proposalBytes common.Hash, yesNo bool) bool {
	
	//必须是合格的出块人
	if _, exist := s.ConfirmedSigners[signer]; !exist {
		return false
	}

	//取自己的最后一张票
	var lastVote *Vote = s.lastVote(signer, proposalBytes)
	
	if lastVote != nil {
		//yesno必须反向之前投的票
		return (!lastVote.YesNo && yesNo) || (lastVote.YesNo && !yesNo)
	} else {
		//自己的第一张票必须是赞成票
		return yesNo
	}
}

/*
执行投票
*/
func (s *Snapshot) cast(signer common.Address, proposalBytes common.Hash, yesNo bool) bool {
	
	proposal := &Proposal{}
	if err:=proposal.fromBytes(proposalBytes);err!=nil {
		return false
	}
	
	//检查提案是否存在
	_, exist := getProposal(proposal.Id)
	if exist!=nil {
		return false
	}
	
	if !s.validVote(signer,proposalBytes, yesNo) {
		return false
	}
	
	if oldVotes, ok := s.Tally[proposalBytes]; ok {
		if yesNo {
			oldVotes++
		} else {
			oldVotes--
		}
		
		if oldVotes <= 0 {
			delete(s.Tally, proposalBytes)
		} else {
			s.Tally[proposalBytes] = oldVotes
		}
	} else {
		//如果是新投票，那么第一张一定是赞成票
		s.Tally[proposalBytes] = 1
	}
	return true
}

/*
撤销执行投票
*/
func (s *Snapshot) uncast(signer common.Address, proposalBytes common.Hash) bool {
	
	proposal := &Proposal{}
	if err :=proposal.fromBytes(proposalBytes);err!=nil {
		return false
	}
	
	//检查提案是否存在
	_, exist := getProposal(proposal.Id)
	if exist!=nil {
		return false
	}
	
	//还没开始投票的提案不算数
	votes, ok := s.Tally[proposalBytes]
	if !ok {
		return false
	} 
	
	//取最后一张票
	var lastVote *Vote = s.lastVote(signer, proposalBytes)
	
	if !lastVote.YesNo {
		return false
	}
	
	// 取消票
	if votes > 1 {
		//减一，因为早前的票已无效
		votes--
		s.Tally[proposalBytes] = votes
	} else {
		//tally.Votes--， 表示tally.Votes变0,所以delete
		delete(s.Tally, proposalBytes)
	}
	
	return true
}

//排序相关的函数和接口
type sortTallyDesc struct {
	Key common.Hash
	Value int
}

type sortTalliesDesc []sortTallyDesc
func (p sortTalliesDesc) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p sortTalliesDesc) Len() int { return len(p) }
func (p sortTalliesDesc) Less(i, j int) bool { return p[i].Value > p[j].Value }

func tallySorter(m map[common.Hash]int) sortTalliesDesc {
	p := make(sortTalliesDesc, len(m))
	i := 0
	for k, v := range m {
		p[i] = sortTallyDesc{k, v}
		i++
	}
	sort.Sort(p)
	return p
}


type sortConfirmedSignerAsc struct {
	Key common.Address
	Value int
}

type sortConfirmedSignersAsc []sortConfirmedSignerAsc
func (p sortConfirmedSignersAsc) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p sortConfirmedSignersAsc) Len() int { return len(p) }
func (p sortConfirmedSignersAsc) Less(i, j int) bool { return p[i].Value < p[j].Value }

func confirmedSignersSorter(m map[common.Address]uint16) sortConfirmedSignersAsc {
	p := make(sortConfirmedSignersAsc, len(m))
	i := 0
	for k, v := range m {
		p[i] = sortConfirmedSignerAsc{k, int(v)}
		i++
	}
	sort.Sort(p)
	return p
}

type sortCandidateVoteDesc struct {
	address common.Address
	weight  *big.Int
}

type sortCandidateVotesDesc []sortCandidateVoteDesc

func (p sortCandidateVotesDesc) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p sortCandidateVotesDesc) Len() int      { return len(p) }
func (p sortCandidateVotesDesc) Less(i, j int) bool {
	if p[i].weight.Cmp(p[j].weight) < 0 {
		return false
	} else if p[i].weight.Cmp(p[j].weight) > 0 {
		return true
	} else {
		return p[i].address.String() < p[j].address.String()
	}
}

func candidateVotesSorter(m map[common.Address]*big.Int) sortCandidateVotesDesc {
	p := make(sortCandidateVotesDesc, len(m))
	i := 0
	for k, v := range m {
		p[i] = sortCandidateVoteDesc{k, v}
		i++
	}
	sort.Sort(p)
	return p
}

// apply creates a new authorization snapshot by applying the given headers to
// the original one.
func (s *Snapshot) apply(chain *core.BlockChain,headers []*types.Header, db ethdb.Database) (*Snapshot, error) {
	
	// Allow passing in no headers for cleaner code
	if len(headers) == 0 {
		return s, nil
	}
	
	// Sanity check that the headers can be applied
	for i := 0; i < len(headers)-1; i++ {
		if headers[i+1].Number.Uint64() != headers[i].Number.Uint64()+1 {
			return nil, errInvalidVotingChain
		}
	}
	
	if headers[0].Number.Uint64() != s.Number+1 {
		return nil, errInvalidVotingChain
	}
	
	// Iterate through the headers and create a new snapshot
	snap := s.copy()
	
	var (
		start  = time.Now()
		logged = time.Now()
	)
	
	for i, header := range headers {
		
		number := header.Number.Uint64()
		
		if number%s.config.EpochInterval == 0 {
			
			//process kickout
			for kickoutSigner := range snap.ConfirmedSigners {
				_, exist := snap.UnconfirmedSigners[kickoutSigner]
				
				if !exist {
					//被踢出者丧失候选人身份
					delete(snap.Candidates, kickoutSigner)
					
					//移除被踢出者投他人的记录
					delete(snap.Delegators, kickoutSigner)
					
					//移除他人投被踢出者的记录
					for delegator, candidate := range snap.Delegators {
						if candidate == kickoutSigner {
							delete(snap.Delegators, delegator)
						}
					}
				}
			}
			
			for k, v := range snap.UnconfirmedProposals {
				snap.ConfirmedProposals[k] = v
			}
			
			snap.ConfirmedSigners = make(map[common.Address]uint16)
			for k := range snap.UnconfirmedSigners {
				snap.ConfirmedSigners[k] = 0
			}
			
			snap.UnconfirmedSigners = make(map[common.Address]struct{})
			snap.UnconfirmedProposals = make(map[uint8]common.Hash)
			
			//在epoch区块时，清除投票信息
			snap.Votes = nil
			snap.Tally = make(map[common.Hash]int)
			
		} 

		/*
		limit这里是指SIGNER_LIMIT,表示一个signer在连续SIGNER_LIMIT个区块内只可以出块一次也等于投人一次
		
		snap.Recents保存最近出块的高度和签名者，所以新块的签名者不能存在于snap.Recents否则无效
		
		打个例子, 签名者列表(SIGNER_COUNT) = 7位人。新块高度=100， SIGNER_LIMIT = FLOOR(SIGNER_COUNT/2) + 1 = 4
		
		删除高度 = 100 - 4 = 96
		删除snap.Recents[96],表示在96高度的这位签名者可以被解放了，他可以在97到100的新块间再签一次
		*/
		if limit := uint64(len(snap.ConfirmedSigners)/2 + 1); number >= limit {
			delete(snap.Recents, number-limit)
		}
		
		//从header signature通过ecrecover(...)取得签名者
		signer, err := ecrecover(header, s.sigcache)
		
		if err != nil {
			return nil, err
		}

		if _, ok := snap.ConfirmedSigners[signer]; !ok {
			return nil, errUnauthorizedSigner
		} else {
			snap.ConfirmedSigners[signer]++
		}
		
		//snap.Recents保证在signer limit个区块间，一个signer只有一个签名
		for _, recent := range snap.Recents {
			if recent == signer {
				return nil, errRecentlySigned
			}
		}
		
		//把签名者加入进snap.Recents里
		snap.Recents[number] = signer
		
		//取是赞成票或是取消票
		var yesNo bool
		switch {
			case bytes.Equal(header.Nonce[:], nonceYesVote):
				yesNo = true
			case bytes.Equal(header.Nonce[:], nonceNoVote):
				yesNo = false
			default:
				return nil, errInvalidVote
		}
		
		//处理当前的票
		if snap.cast(signer, header.MixDigest, yesNo) {
			snap.Votes = append(snap.Votes, &Vote{
				Signer:   signer,
				Block:    number,
				Proposal: header.MixDigest,
				YesNo:    yesNo,
			})
		}
		
		//添加新delegator
		block := chain.GetBlock(header.Hash(), number)
		
		//由于eth是先同步块头后同步块体，返回错误是因为块体还未完成同步
		if block == nil {
			//return nil, errMissingBody
		} else {
			txs := block.Body().Transactions
			if len(txs) == 0 {
				//return nil, errMissingBody
			} else {
				
				ethSigner := types.MakeSigner(chain.Config(), new(big.Int).SetUint64(number))
				
				for i:=0; i < len(txs); i++ {
					tx := txs[i]
					
					if *tx.To() == contractAddress {
						action:= &Action{}
						if err := action.fromBytes(tx.Data()); err == nil {
							
							if from, err := ethSigner.Sender(tx); err == nil {

								switch action.Id {
									case becomeCandidate:
										snap.Candidates[from] = struct{}{}
									
									case becomeDelegator:
										
										candidate := action.Values[0].(common.Address)
										
										_, exist := snap.Candidates[candidate]
										
										if exist {
											snap.Delegators[from] = candidate
										}
										
									case quitCandidate:
										delete(snap.Candidates,from)
										
									case quitDelegator:
										delete(snap.Delegators,from)
								}
							}
						}
					}
				}
			}
		}
		
		if (number+1)%s.config.EpochInterval == 0 {
			
			statedb, err := state.New(header.Root, state.NewDatabase(db), nil)
			if err!=nil {
				return nil, err
			}
			
			//这里预选新签名者
			
			//每个出块人的最低出块数，低过这个值将被开除, -1 是不包括epoch块
			minMintTarget := (int(snap.config.EpochInterval) - 1) / len(snap.ConfirmedSigners) / 2
			candidateCnt := len(snap.Candidates) - len(snap.ConfirmedSigners)
			candidateCnt = 1
			sorted := confirmedSignersSorter(snap.ConfirmedSigners)
			
			//被踢出签名者,将丧失候选人身份
			kickoutSigners := make(map[common.Address]struct{},0)
			candidateVotes := make(map[common.Address]*big.Int)
			
			for _, kv := range sorted {
				
				address := kv.Key
				mintCnt := kv.Value
				
				if len(kickoutSigners) < candidateCnt && mintCnt < minMintTarget {
					
					kickoutSigners[address] = struct{}{}
					
					log.Info("Signer has been kickout", "signer", address, "mintCnt", mintCnt, "minMintTarget", minMintTarget)
				} else {
					candidateVotes[ address ] = big.NewInt(0)
				}
			}
			
			//如今 snap.Candidates都是合格的候选人， 开始竞争!
			
			//计算每个候选人能获得的支持率
			
			var exist bool
			for delegator, candidate := range snap.Delegators {
				_, exist = kickoutSigners[candidate]
				
				if exist {
					continue
				}
				
				_, exist = kickoutSigners[delegator]
				
				if exist {
					continue
				}
				
				_, exist = candidateVotes[candidate]
				
				if !exist {
					candidateVotes[candidate] = big.NewInt(0)
				}
				
				balance := statedb.GetBalance(delegator)
				if balance.Cmp(common.Big0) > 0 {
					candidateVotes[candidate].Add(candidateVotes[candidate],balance)
				}
			}
			
			newSigners := candidateVotesSorter(candidateVotes)
			fmt.Println("")
			if len(newSigners) > maxSignerSize {
				newSigners = newSigners[:maxSignerSize]
			}
			
			epochCnt := (number+1)/s.config.EpochInterval
			seed := int64(binary.LittleEndian.Uint32(crypto.Keccak512(header.Hash().Bytes()))) + int64(epochCnt)
			r := rand.New(rand.NewSource(seed))
			for i := len(newSigners) - 1; i > 0; i-- {
				j := int(r.Int31n(int32(i + 1)))
				newSigners[i], newSigners[j] = newSigners[j], newSigners[i]
			}
			
			for _, newSigner := range newSigners {
				snap.UnconfirmedSigners[newSigner.address] = struct{}{}
			}
			
			//由于相同的提案ID但不同的值（子提案）是可以做多，这里按ID把同类型的提案重新组合
			groupProposals := make(map[uint8]map[common.Hash]int)
			for proposalBytes, votes := range snap.Tally {
				
				proposal := &Proposal{}
				if err:=proposal.fromBytes(proposalBytes);err!=nil {
					return nil, err
				}
				
				_, exist := groupProposals[proposal.Id]
				
				if !exist {
					groupProposals[proposal.Id] = make(map[common.Hash]int)
				} 
				groupProposals[proposal.Id][proposalBytes] = votes
				
			}
			
			//把获票最多的子提案，按提案ID存入 selectedProposals
			selectedProposals := make(map[uint8]common.Hash)
			for proposalId, proposalVotes := range groupProposals {
				
				if len(proposalVotes) > 1 {
					sorted := tallySorter(proposalVotes)
					
					//如果同类的两个子提案的获得票是相同的，那么这个提案将无效
					if sorted[0].Value == sorted[1].Value {
						continue
					}
			
					selectedProposals[proposalId] = sorted[0].Key
				} else {
					for proposalBytes := range proposalVotes {
						selectedProposals[proposalId] = proposalBytes
					}
				}
			}
			
			//将当前已定案的值拷贝到UnconfirmedProposals
			for k, v := range snap.ConfirmedProposals {
				snap.UnconfirmedProposals[k] = v
			}
			
			//将由投票产出的结果写入UnconfirmedProposals
			for proposalId, proposalBytes := range selectedProposals {
				snap.UnconfirmedProposals[proposalId] = proposalBytes
			}
			
			//UnconfirmedProposals将在epoch区块被处理
		}
		
		//如果process时间过长就写入日志
		if time.Since(logged) > 8*time.Second {
			log.Info("Reconstructing voting history", "processed", i, "total", len(headers), "elapsed", common.PrettyDuration(time.Since(start)))
			logged = time.Now()
		}
	}
	
	//如果process时间过长就写入日志
	if time.Since(start) > 8*time.Second {
		log.Info("Reconstructed voting history", "processed", len(headers), "elapsed", common.PrettyDuration(time.Since(start)))
	}
	
	snap.Number += uint64(len(headers))
	snap.Hash = headers[len(headers)-1].Hash()
	
	return snap, nil
}


func (s *Snapshot) unconfirmedSigners() []common.Address {
	sigs := make([]common.Address, 0, len(s.UnconfirmedSigners))
	for sig := range s.UnconfirmedSigners {
		sigs = append(sigs, sig)
	}
	sort.Sort(signersAscending(sigs))
	return sigs
}

// signers retrieves the list of authorized signers in ascending order.
func (s *Snapshot) confirmedSigners() []common.Address {
	sigs := make([]common.Address, 0, len(s.ConfirmedSigners))
	for sig := range s.ConfirmedSigners {
		sigs = append(sigs, sig)
	}
	sort.Sort(signersAscending(sigs))
	return sigs
}

func (s *Snapshot) unconfirmedProposals() []common.Hash {
	keys := make([]int, len(s.UnconfirmedProposals))
	
	ascSortedProposals := make([]common.Hash, 0, len(s.UnconfirmedProposals))
	
	i := int(0)
    for k := range s.UnconfirmedProposals {
        keys[i] = int(k)
        i++
    }
    sort.Ints(keys)
	
	for _, k := range keys {
		ascSortedProposals = append(ascSortedProposals, s.UnconfirmedProposals[uint8(k)])
    }
	
	return ascSortedProposals
}

func (s *Snapshot) confirmedProposals() []common.Hash {
	
	keys := make([]int, len(s.ConfirmedProposals))
	
	sortedProposals := make([]common.Hash, 0, len(s.ConfirmedProposals))
	
	i := int(0)
    for k := range s.ConfirmedProposals {
        keys[i] = int(k)
        i++
    }
    sort.Ints(keys)
	
	for _, k := range keys {
		sortedProposals = append(sortedProposals, s.ConfirmedProposals[uint8(k)])
    }
	
	return sortedProposals
}

// inturn returns if a signer at a given block height is in-turn or not.
func (s *Snapshot) inturn(number uint64, signer common.Address) bool {
	signers, offset := s.confirmedSigners(), 0
	for offset < len(signers) && signers[offset] != signer {
		offset++
	}
	return (number % uint64(len(signers))) == uint64(offset)
}
