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

/*
dpos包实现DPOS共识引擎
 
dpos是基于clique上开发的DPOS
*/
package dpos

import (
	"bytes"
	"errors"
	
	"math/big"
	"math/rand"
	"sync"
	"time"
	"fmt"
	
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	_ "github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	lru "github.com/hashicorp/golang-lru"
)

//dpos常量
const (
	signerReward    = 50     //签名者的奖励%份额, 必须是int
	maxSignerSize  = 2		  //最多多少个signer在一个epoch世代
	storeSnapInterval = 1024  //块高度%storeSnapInterval==0时，快照将存入DB
	inmemorySnapshots  = 128  //缓存存入多少个最近的快照
	inmemorySignatures = 4096 //缓存存入多少个ecrecover的结果

	wiggleTime = 500 * time.Millisecond //如果出块轮不到我，多等待一会，减少链脏（不必要的侧链插入)
)

var (
	
	FrontierBlockReward       = big.NewInt(5e+18) 
	ByzantiumBlockReward      = big.NewInt(3e+18) 
	ConstantinopleBlockReward = big.NewInt(2e+18) 
	
	epochLength = uint64(30000) //块高度%epochlength==0时，这块便是创世块

	//填充block.header.nonce值
	nonceYesVote = hexutil.MustDecode("0xffffffffffffffff") //投赞成票
	nonceNoVote = hexutil.MustDecode("0x0000000000000000")  //投取消赞成票
	
	uncleHash = types.CalcUncleHash(nil) // Keccak256(RLP([])),叔块在dpos是没有意义的

	diffInTurn = big.NewInt(2) // 轮到我(in-turn)的难度
	diffNoTurn = big.NewInt(1) // 轮到其他人(no-turn)的难度
)

var (
	//区块不存在于本地
	errUnknownBlock = errors.New("unknown block")

	//epoch区块不允许投票
	errInvalidEpochVoting = errors.New("Voting does not allow in epoch block")
	
	//非epoch区块的extra只能是签名
	errInvalidNonEpochExtra = errors.New("Non epoch block's extra only allow signature field")
	
	//epoch区块的extra的proposals不符合条件
	errInvalidEpochExtraSigner = errors.New("Invalid signers contain in epoch block's extra")
	
	//epoch区块的extra的signers不符合条件
	errInvalidEpochExtraProposal = errors.New("Invalid proposals contain in epoch block's extra")

	//nonces值只能是0x00..0或0xff..f
	errInvalidVote = errors.New("vote nonce not 0x00..0 or 0xff..f")

	//epoch区块的nonce值只能是0x00..0
	errInvalidEpochVote = errors.New("vote nonce in epoch block non-zero")

	//签名格式不符合
	errMissingSignature = errors.New("extra-data 65 byte signature suffix missing")

	//epoch区块的signer与本地的signer不配对
	errInvalidEpochSigners = errors.New("invalid signer list on epoch block")

	//本地与入参的签名者不配对
	errMismatchingEpochSigners = errors.New("mismatching signer list on epoch block")

	//叔块不是空
	errInvalidUncleHash = errors.New("non empty uncle hash")

	//难度不是1或2
	errInvalidDifficulty = errors.New("invalid difficulty")

	//难度值不对，比如轮到我，但块难度是1,或轮不到我，但块难度是2
	errWrongDifficulty = errors.New("wrong difficulty")

	//新区块的时间截不能大过父区块 + slotinterval
	errInvalidTimestamp = errors.New("invalid timestamp")

	// 用在snapshot, 检查入参的headers(多祖先块）是否合格
	errInvalidVotingChain = errors.New("invalid voting chain")

	//当前signer不属于合格的签名者
	errUnauthorizedSigner = errors.New("unauthorized signer")

	//同一个签名者只能在signer limit个区块里出一次块，否则便会收到以下的错误
	errRecentlySigned = errors.New("recently signed")
	
	//Mint创世块时, 块体还没有同步
	errMissingBody = errors.New("Missing body")
)

// SignerFn hashes and signs the data to be signed by a backing account.
type SignerFn func(signer accounts.Account, mimeType string, message []byte) ([]byte, error)

// dpos是基于clique上开发的dpos
type Dpos struct {
	config *params.DposConfig // Dpos引擎的参数
	db     ethdb.Database       // DB用来操作快照 snapshot

	recents    *lru.ARCCache    // 快速读取最近的Snapshots，以达到加速处理reorg的目的
	signatures *lru.ARCCache    // 快速读取最近的Signatures，以达到加速处理mining的目的

	myProposals map[common.Hash]bool //键值为proposal bytes

	signer common.Address       // signer的以太坊地址
	signFn SignerFn             // signer的签名函数
	lock   sync.RWMutex         // 加锁保护signer字段

	//以下测试用途
	fakeDiff bool //跳过难度验证
}

func New(config *params.DposConfig, db ethdb.Database) *Dpos {
	// Set any missing consensus parameters to their defaults
	conf := *config
	if conf.EpochInterval == 0 {
		//如果链配置是空，那就使用默认值
		conf.EpochInterval = epochLength
	}
	// Allocate the snapshot caches and create the engine
	recents,    _ := lru.NewARC(inmemorySnapshots) //最近的Snapshots
	signatures, _ := lru.NewARC(inmemorySignatures)//最近的Signatures
	
	//返回一个新的Dpos对象
	return &Dpos{
		config:     &conf,
		db:         db,
		recents:    recents,
		signatures: signatures,
		myProposals:  make(map[common.Hash]bool),
	}
}

/*
入参自己的etherbase和签名函数
*/
func (self *Dpos) Authorize(signer common.Address, signFn SignerFn) {
	self.lock.Lock()
	defer self.lock.Unlock()

	self.signer = signer
	self.signFn = signFn
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) Author(header *types.Header) (common.Address, error) {
	return ecrecover(header, self.signatures)
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header, seal bool) error {
	return self.verifyHeader(chain, header, nil)
}

/*
实现 consensus.Engine 接口

VerifyHeaders专门处理多区块头。

*/
func(self *Dpos) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	//返回abort通道给调用者终止验证
	abort := make(chan struct{})
	
	//返回result通道，用来传送验证结果
	results := make(chan error, len(headers))

	//开启协程，利用通道与调用者沟通
	go func() {
		for i, header := range headers {
			err := self.verifyHeader(chain, header, headers[:i])

			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

/*
实现 consensus.Engine 接口

Dpos没有叔块的概念，如果区块存在叔块，那么就返回错误
*/
func(self *Dpos) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	if len(block.Uncles()) > 0 {
		return errors.New("uncles not allowed")
	}
	return nil
}

func (self *Dpos) verifyHeader(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	
	//区块高度不可以空
	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()
	
	//不接受未来块
	if header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}
	
	//epoch区块里不能有投票信息
	epochBlock := (number % self.config.EpochInterval) == 0
	if epochBlock && header.MixDigest != (common.Hash{}) {
		return errInvalidEpochVoting
	}

	//Nonces值只能是0x00..0或0xff..f, 而epoch区块则只能是0x00 (nonceNoVote)
	if !bytes.Equal(header.Nonce[:], nonceYesVote) && !bytes.Equal(header.Nonce[:], nonceNoVote) {
		return errInvalidVote
	}

	if epochBlock && !bytes.Equal(header.Nonce[:], nonceNoVote) {
		return errInvalidEpochVote
	}
	
	//验证extra值
	
	//全部区块的extra的开头都必定是0x41
	var extras [][]byte
	
	if header.Extra[0] != 0x41 {
		return errMissingSignature
	//非epoch区块的extra只能是签名
	} else if !epochBlock && len(header.Extra) != crypto.SignatureLength + 1 {
		return errInvalidNonEpochExtra
	} else if epochBlock {
		extras = unserialize(header.Extra)
		
		//至少需要一个signer,注意这里还未深入验证
		if !(len(extras[1])%common.AddressLength ==0 && len(extras[1])/common.AddressLength > 0) {
			return errInvalidEpochExtraSigner
		}
		
		//必须和Proposals的数量对等
		if !(len(extras[2])%common.HashLength ==0 && len(extras[2])/common.HashLength == len(Proposals)) {
			return errInvalidEpochExtraProposal
		} else {
						
			proposalCnt := len(extras[2])/common.HashLength
			
			//检查每个提案的值
			for i := 0; i < proposalCnt; i++ {
				proposal := &Proposal{}
				if err := proposal.fromBytes(common.BytesToHash(extras[2][i*common.HashLength:(i+1)*common.HashLength])); err != nil {
					return err
				}
			}
		}
	}
	
	//叔块必需是空
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}
	
	//难度值必须是可接受值，注意这里还未深入验证
	if number > 0 {
		if header.Difficulty == nil || (header.Difficulty.Cmp(diffInTurn) != 0 && header.Difficulty.Cmp(diffNoTurn) != 0) {
			return errInvalidDifficulty
		}
	}
	
	//取父块
	var parent *types.Header
	if len(parents) > 0 {
		parent = parents[len(parents)-1]
	} else {
		parent = chain.GetHeader(header.ParentHash, number-1)
	}
	
	//检查父块是否合格
	if parent == nil || parent.Number.Uint64() != number-1 || parent.Hash() != header.ParentHash {
		return consensus.ErrUnknownAncestor
	}
	
	//新区块的时间截不能大过父区块 + slotinterval
	if parent.Time+self.config.SlotInterval > header.Time {
		return errInvalidTimestamp
	}
	
	//取最近的快照
	snap, err := self.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		return err
	}
	
	if epochBlock {
		
		//把本地签名者生成bytes (list)
		signers := make([]byte, len(snap.UnconfirmedSigners)*common.AddressLength)
		for i, signer := range snap.unconfirmedSigners() {
			copy(signers[i*common.AddressLength:], signer[:])
		}
		
		//比较本地与入参的签名者是否一样
		if !bytes.Equal(signers, extras[1]) {
			return errMismatchingEpochSigners
		}
	}
	
	return self.verifySeal(chain, header, parents)
}


/*
实现 consensus.Engine 接口
*/
func(self *Dpos) VerifySeal(chain consensus.ChainHeaderReader, header *types.Header) error {
	return self.verifySeal(chain, header, nil)
}


func (self *Dpos) verifySeal(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	
	//不接受创世块
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	
	//拿最近的快照
	snap, err := self.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		return err
	}
	
	//检查签名者是否合格
	signer, err := ecrecover(header, self.signatures)
	if err != nil {
		return err
	}
	if _, ok := snap.ConfirmedSigners[signer]; !ok {
		return errUnauthorizedSigner
	}
	
	//检查签名者是否在signer limit个区块里多过一次出块
	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only fail if the current block doesn't shift it out
			if limit := uint64(len(snap.ConfirmedSigners)/2 + 1); seen > number-limit {
				return errRecentlySigned
			}
		}
	}
	
	//正式检查难度
	if !self.fakeDiff {
		inturn := snap.inturn(header.Number.Uint64(), signer)
		//属inturn的signer必须给对应的难度#2
		if inturn && header.Difficulty.Cmp(diffInTurn) != 0 {
			return errWrongDifficulty
		}
		
		//属noturn的signer必须给对应的难度#1
		if !inturn && header.Difficulty.Cmp(diffNoTurn) != 0 {
			return errWrongDifficulty
		}
	}
	
	return nil
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) Prepare(chain consensus.ChainHeaderReader, header *types.Header/*新块头*/) error {
	//初始化header(区块头)
	
	//记录提案
	header.MixDigest = common.Hash{}
	
	//在clique这是被投人
	header.Coinbase = common.Address{}
	
	//投yes|no票
	header.Nonce = types.BlockNonce{}
	
	//新区块高度
	number := header.Number.Uint64()
	
	//Assemble the voting snapshot to check which votes make sense
	//取最近的snapshot
	snap, err := self.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}
	
	//如果新块不是epoch区块
	if number%self.config.EpochInterval != 0 {
		self.lock.RLock()
		
		validProposals := make([]common.Hash, 0, len(self.myProposals))
		for proposalBytes, yesNo := range self.myProposals {
			if snap.validVote(self.signer, proposalBytes, yesNo) {//投过的提案将被除外
				validProposals = append(validProposals, proposalBytes)
			}
		}

		if len(validProposals) > 0 {
			//随机抽个自己想投的票
			r := rand.Intn(len(validProposals));//注意 panics if n <= 0
			
			for i, proposalBytes := range validProposals {
				if r == i {
					header.MixDigest = proposalBytes
					
					yesNoVote := self.myProposals[proposalBytes]
					if yesNoVote {
						copy(header.Nonce[:], nonceYesVote)
					} else {
						copy(header.Nonce[:], nonceNoVote)
					}
					break;
				}
			}
		}
		self.lock.RUnlock()
	}
	
	/*
	计算难度
	*/
	header.Difficulty = calcDifficulty(snap, self.signer) 
	
	/*
	处理 block.header.extra
	*/
	header.Extra = make([]byte,0)
	
	//初始化签名值为0x00...0
	item := bytes.Repeat([]byte{0x00}, crypto.SignatureLength)
	
	header.Extra = append(header.Extra, VarIntToBytes(item)...)
	header.Extra = append(header.Extra, item...)
	
	//epoch区块
	if number%self.config.EpochInterval == 0 {
		
		item = make([]byte,0)
		
		//添加多签名者的信息, 按地址排序
		for _, signer := range snap.unconfirmedSigners() {
			item = append(item, signer[:]...)
		}
		
		header.Extra = append(header.Extra, VarIntToBytes(item)...)
		header.Extra = append(header.Extra, item...)
		
		item = make([]byte,0)
		
		//添加提案结果的信息
		for _, proposalBytes := range snap.unconfirmedProposals() {
			item = append(item, proposalBytes.Bytes()...)
		}
		
		header.Extra = append(header.Extra, VarIntToBytes(item)...)
		header.Extra = append(header.Extra, item...)
	}
	
	//更新正确的时间截
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	
	header.Time = parent.Time + self.config.SlotInterval
	//header.Time 等于 max(parent.Time + Period, now) 
	if header.Time < uint64(time.Now().Unix()) {
		header.Time = uint64(time.Now().Unix())
	}
	
	return nil
}


/*
实现 consensus.Engine 接口

使用场景是： 

1) 模拟区块链作测试用途。 GenerateChain(...) @ core/chain_makers.go
2) 每当接收新区块时，程序在处理完全部txs后将调用。 StateProcessor.Process(...) @ core/state_processor.go
3) 创建work的时候调用。 worker.commit(...) @ miner/worker.go

*/
func(self *Dpos) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
		uncles []*types.Header) {
	
	number := header.Number.Uint64()
	
	//读取应得的奖励
	blockReward := FrontierBlockReward
	
	if chain.Config().IsByzantium(header.Number) {
		blockReward = ByzantiumBlockReward
	}
	if chain.Config().IsConstantinople(header.Number) {
		blockReward = ConstantinopleBlockReward
	}
	
	//如果是下载的块，signer一定会有值
	signer, _ := ecrecover(header, self.signatures); 
	
	if signer == (common.Address{}) {
		//否则这是miner打造的新区块
		signer = self.signer 
	} 
	
	//把奖励发给签名者
	toSigner := new(big.Int).Set(blockReward)
	toSigner.Mul(toSigner, big.NewInt(signerReward))
	toSigner.Div(toSigner, big.NewInt(100))
	
	state.AddBalance(signer, toSigner)
	
	//把奖励发给委托人
	toDelegators :=  new(big.Int).Set(blockReward)
	toDelegators.Sub(toDelegators, toSigner)
	
	/*
	找出入参的块头属于哪个epoch块
	*/
	var currentEpochBlock uint64
	if number%self.config.EpochInterval == 0 {
		currentEpochBlock = number - self.config.EpochInterval
	} else {
		currentEpochBlock = number - (number%self.config.EpochInterval)
	}
	
	if currentEpochBlock > 0 {
		fmt.Println("chiew check currentEpochBlock", currentEpochBlock, "number", number)
		
		/*
		向后循环直到找到currentEpochBlock-1的区块，为什么是currentEpochBlock-1，因为这个区块的snap里记录着投currentepoch签名者的委托人
		*/
		searchNumber := number - 1
		searchHash := header.ParentHash
		for searchNumber != currentEpochBlock - 1 {
			header := chain.GetHeader(searchHash, searchNumber)
			searchNumber, searchHash = searchNumber - 1, header.ParentHash
		}

		//取snap.Delegators，他们是获利者 
		snap, _ := self.snapshot(chain, searchNumber , searchHash, nil)
		
		qualifiedDelegators := []common.Address{}
		for delegator, candidate := range snap.Delegators {
			if candidate == signer {
				qualifiedDelegators = append(qualifiedDelegators, delegator)
			}
		}
		
		totalDelegators := int64(len(qualifiedDelegators))
		
		if totalDelegators > 0 {
			//均分奖励，每人可得的份额
			delegatorReward := toDelegators.Div(toDelegators, big.NewInt(totalDelegators)) 

			if delegatorReward.Cmp(common.Big0) > 0 {
				for _, delegator := range qualifiedDelegators {
					state.AddBalance(delegator, delegatorReward)
				}
				
			}
		}
	}
	
	/*计算world state trie并更新 header.Root*/
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	
	/*uncle不存在于Dpos*/
	header.UncleHash = types.CalcUncleHash(nil)
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {

	self.Finalize(chain, header, state, txs, uncles)
	
	//返回一个未完成的区块，等待sealing(签名)
	return types.NewBlock(header, txs, nil, receipts, new(trie.Trie)), nil
	
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	
	header := block.Header()
	
	//创世块不处理
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	
	//不接受空tx
	/*
	if len(block.Transactions()) == 0 {
		log.Info("Sealing paused, waiting for transactions")
		return nil
	}
	*/
	
	//避免脏读
	self.lock.RLock()
	signer, signFn := self.signer, self.signFn
	self.lock.RUnlock()
	
	//再确定自己是否是合格的签名者
	snap, err := self.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}
	
	if _, authorized := snap.ConfirmedSigners[signer]; !authorized {
		return errUnauthorizedSigner
	}
	
	//奇怪，这个snap.Recents的检查不是在dpos.Prepare()里发生过了吗
	
	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only wait if the current block doesn't shift it out
			if limit := uint64(len(snap.ConfirmedSigners)/2 + 1); number < limit || seen > number-limit {
				log.Info("Signed recently, must wait for others")
				return nil
			}
		}
	}
	
	
	delay := time.Unix(int64(header.Time), 0).Sub(time.Now()) // nolint: gosimple
	if header.Difficulty.Cmp(diffNoTurn) == 0 {
		
		/*
		如果自己没有出块优先权，多等待点时间，这就优先给出块人出块权：
		
		所以如果拥有出块权的签名者掉线，其他签名者还是可以签发的
		*/
		wiggle := time.Duration(len(snap.ConfirmedSigners)/2+1) * wiggleTime
		delay += time.Duration(rand.Int63n(int64(wiggle)))

		log.Trace("Out-of-turn signing requested", "wiggle", common.PrettyDuration(wiggle))
	}
	
	/*
	签发块
	*/
	sighash, err := signFn(accounts.Account{Address: signer}, accounts.MimetypeDpos, RLP(header))
	
	
	if err != nil {
		return err
	}
	
	/*
	把签名写入extra字段
	*/
	extras := unserialize(header.Extra)
	header.Extra = make([]byte,0)
	
	copy(extras[0][:], sighash)
	for _, extra := range extras {
		header.Extra = append(header.Extra, VarIntToBytes(extra)...)
		header.Extra = append(header.Extra, extra...)
	}

	//最后，等待seal程序被终止或触发delay超时
	log.Trace("Waiting for slot to sign and propagate", "delay", common.PrettyDuration(delay))

	go func() {
		select {
			case <-stop:
				return
			//如果有延迟（diffNoTurn),这里会等待并解放
			case <-time.After(delay):
		}

		select {
			case results <- block.WithSeal(header):
			default:
				log.Warn("Sealing result is not read by miner", "sealhash", SealHash(header))
		}
	}()

	return nil
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) SealHash(header *types.Header) common.Hash {
	return SealHash(header)
}

/*
实现 consensus.Engine 接口

DIFF_NOTURN(2) if BLOCK_NUMBER % SIGNER_COUNT != SIGNER_INDEX
DIFF_INTURN(1) if BLOCK_NUMBER % SIGNER_COUNT == SIGNER_INDEX
*/
func(self *Dpos) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	snap, err := self.snapshot(chain, parent.Number.Uint64(), parent.Hash(), nil)
	if err != nil {
		return nil
	}
	return calcDifficulty(snap, self.signer)
}

func calcDifficulty(snap *Snapshot, signer common.Address) *big.Int {
	if snap.inturn(snap.Number+1, signer) {
		return new(big.Int).Set(diffInTurn)
	}
	return new(big.Int).Set(diffNoTurn)
}

/*
实现 consensus.Engine 接口

type API struct {
	Namespace string      //命名空间, 可以通过dpos.xxx访问
	Version   string      //版本
	Service   interface{} //dpos API对象
	Public    bool        //是否提供给外部接入?如果是false,那么只是console里可以调用
}
*/
func(self *Dpos) APIs(chain consensus.ChainHeaderReader) []rpc.API {

	return []rpc.API{{
		Namespace: "dpos",
		Version:   "1.0",
		Service:   &API{chain: chain, dpos: self},
		Public:    false,
	}}
	
}

/*
实现 consensus.Engine 接口
*/
func(self *Dpos) Close() error {
	return nil
}

/*
跟据区块高度 number uint64去找最近的snapshot。snapshot是指在某个区块区间的状态，主要状态包括合格出块人、投票统计等等
*/
func (self *Dpos) snapshot(chain consensus.ChainHeaderReader, number uint64, hash common.Hash, parents []*types.Header) (*Snapshot, error) {
	
	var (
		headers []*types.Header
		snap    *Snapshot
	)
	
	for snap == nil { //一直努力寻找最近的snapshot
	
		//试试在内存里找
		/*
		if s, ok := self.recents.Get(hash); ok {
			snap = s.(*Snapshot)
			break
		}
		*/
		
		//试试在磁盘里找
		if number%storeSnapInterval == 0 {
			if s, err := loadSnapshot(self.config, self.signatures, self.db, hash); err == nil {
				log.Trace("Loaded voting snapshot from disk", "number", number, "hash", hash)
				snap = s
				break
			}
		}
		
		/*
		内存和磁盘都找不到snap, 那么唯有创建新快照
		
		如果是创世块(创世块也是epoch区块) 或
		是epoch区块 和 区块数多过 params.FullImmutabilityThreshold (90000) 或
		是epoch区块 和 父区块是nil??
		*/
		if number == 0 || (number%self.config.EpochInterval == 0 && (len(headers) > params.FullImmutabilityThreshold || chain.GetHeaderByNumber(number-1) == nil)) {
			
			
			epochBlockHeader := chain.GetHeaderByNumber(number)
			if epochBlockHeader != nil {
				hash := epochBlockHeader.Hash()

				extras := unserialize(epochBlockHeader.Extra)
				signers := make([]common.Address, len(extras[1])/common.AddressLength)
				
				for i := 0; i < len(signers); i++ {
					copy(signers[i][:], extras[1][i*common.AddressLength:])
				}
				
				proposalCnt := len(extras[2])/common.HashLength
				proposals := make([]*Proposal, 0)
				
				for i := 0; i < proposalCnt; i++ {
					proposal := &Proposal{}
					if err := proposal.fromBytes(common.BytesToHash(extras[2][i*common.HashLength:(i+1)*common.HashLength])); err!= nil {
						return nil, err
					}
					
					proposals = append(proposals, proposal)
				}
				
				snap = newSnapshot(self.config, self.signatures, number, hash, signers, proposals)
				
				if err := snap.store(self.db); err != nil {
					return nil, err
				}
				log.Info("Stored epoch snapshot to disk", "number", number, "hash", hash)
				break
			}
		}
		
		var header *types.Header
		//如果找到入参的parents,那么使用它继续往后找
		if len(parents) > 0 {
			header = parents[len(parents)-1]
			if header.Hash() != hash || header.Number.Uint64() != number {
				return nil, consensus.ErrUnknownAncestor
			}
			parents = parents[:len(parents)-1]			
		} else {
			//没有则从db里读取吧
			header = chain.GetHeader(hash, number)
			if header == nil {
				return nil, consensus.ErrUnknownAncestor
			}
		}
		
		//把全部走过的区块都存在于headers,之后要给snap.apply做统计用的
		headers = append(headers, header)
		number, hash = number-1, header.ParentHash
	}
	
	//把headers中保存的头从按高度从小到大排序，其实就是做reverse处理。
	for i := 0; i < len(headers)/2; i++ {
		headers[i], headers[len(headers)-1-i] = headers[len(headers)-1-i], headers[i]
	}
	
	//处理投票
	
	snap, err := snap.apply(chain.(*core.BlockChain), headers, self.db) 
	if err != nil {
		return nil, err
	}
	
	self.recents.Add(snap.Hash, snap)
	
	//每每达到storeSnapInterval(1024)个区块时就存入db
	if snap.Number%storeSnapInterval == 0 && len(headers) > 0 {
		if err = snap.store(self.db); err != nil {
			return nil, err
		}
		log.Trace("Stored voting snapshot to disk", "number", snap.Number, "hash", snap.Hash)
	}
	
	return snap, err
}
