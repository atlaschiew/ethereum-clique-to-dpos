<p align="center">
    <h3 align="center">以太坊DPOS</h3>
    <p align="center">以clique共识改编而成的以太坊DPOS。本章以中文书写。 任何有误的地方，请不吝指教。</p> 
</p>
<br/>
<br/>

## 目录
* [设计思路](#设计思路)
* [安装部署](#deployment)
* [源码分析](#coding-study)
  * [项目结构](#project-structure)
  * [出块流程](#block-production)
  * [验证流程](#block-verification)
  * [快照](#snapshot)
  * [选举](#voting)
  * [API](#api)  
* [后语](#ps) 

## 设计思路
> 本项目采用 [geth v1.9.25](https://github.com/ethereum/go-ethereum/tree/v1.9.25) 。

作者采用geth的版本是1.9.25,理由是：
1. 这版本删除了whisper协议这个没必要的插入。
2. 网上能搜索到的源码分析文章更多是针对1.8.x版本的，而1.9.25的更改相较于1.10.x更贴近1.8.x，所以更容易读懂。
3. 1.9.x的最后一个版本，相信程序更稳定。

设计原则：
1. 不破坏transaction结构体。
2. 不破坏block结构体。如clique，重新定义一些block的字段。
3. 更改尽量发生在clique框架内。
4. 善用已有的接口和函数。

和clique的相同之处:
1. SIGNER_LIMIT概念, 指签名者这次签名后得等SIGNER_LIMIT个区块后才可以再签名发块。SIGNER_LIMIT = floor(SIGNER_COUNT / 2) + 1。
2. DIFF_NOTURN/DIFF_INTURN概念，指轮到我签名时，header.difficulty=2,否则是1。作用是如果准签名者不在时，其他签名者可以补发，但优先权还是给准签名者。 
3. 都有Epoch和slot概念, clique的slot的别名是period。

和clique的不同之处:
1. 选新签名者不再由当前签名者通过clique.API.Propose(address,auth)提拔，取而代之的是广大用户可以通过tx提拔候选人。
2. dpos.API.Propose(value, yesno),继而改成提案功能。dpos.Proposals记录着所有可投票的提案(默认只有dpos.Proposals.TestProposal一个)。这些提案可以用作系统配置。
3. clique的epoch块只记录下一轮的合法签名者(signer),而dpos还另加两项: 合法委托人(delegator)和提案结果。
4. 选举过程的不同。clique的候选人只要得到半数票 （tally.Votes > len(snap.Signers)/2）时，候选人便马上生效成签名者。在dpos, 候选人得等到epoch时才根据delegators的余额比重去选其所投的签名者。
5. 签名者在成功发块后将获得奖励，而投此签名者的委托人也一样会获得奖励。

设计难度
1. 刚才提到了选举过程中用到了delegator的余额，而这将涉及到state。需知state不是持久储存的，旧块的state root会找不到。
2. 由于clique不涉及state, 记录和验证工作都在header level,这样做的好处就算在fast/light模式也能很好运作。
3. 在fast sync模式的pivot的作用下，由于 <= pivot的块将不回放state, 这将导致dpos.snapshot无法正确统计。再生成state又违反fast sync的初衷。

## 安装部署
### 下载编译
> ✅ go version >= 1.16
1. cd切换到你想要的path,然后下载源码
```sh
$ cd ~ 
$ git clone https://github.com/atlaschiew/ethereum-clique-to-dpos.git

# 重新命名文件夹，取dpos这个更短的名字
$ mv ethereum-clique-to-dpos dpos
```
2. 编译
```sh
$ cd dpos
$ make geth

# 成功后
$ ls build/bin
abidump          bootnode devp2p faucet      abigen    
checkpoint-admin ethkey   geth   puppethclef evm 
p2psim           rlpdump

# 最重要的二进制文件便是geth,另外bootnode待会也会用到
```
3. 启动3个节点，他们分别为full节点、fast sync节点和light节点。
4. 为3个节点各创建一个专属的数据库文件夹。
```sh
$ mkdir ~/db_dpos/db1
$ mkdir ~/db_dpos/db2
$ mkdir ~/db_dpos/db3
```
5. 为3个节点各创建一个账号
```sh
# 为了方便测试，这里手动创建 keystore

# 第1个节点的miner.etherbase为 0x0D4A5c97AACe5D2B60Bf0859366450a1A46BC680
$ mkdir ~/db_dpos/db1/keystore
$ echo '{"address":"0d4a5c97aace5d2b60bf0859366450a1a46bc680","crypto":{"cipher":"aes-128-ctr","ciphertext":"9b35cf84cc7e4803257264cbaf47aa2b6e502098e01cd56e0c306c702cc6208b","cipherparams":{"iv":"42647d1fab0d137eb84a70267700e326"},"kdf":"scrypt","kdfparams":{"dklen":32,"n":262144,"p":1,"r":8,"salt":"29f93f9e7491a69e9f456ac23d74b47f1ce3c8b5d2d083817114b15286137b7e"},"mac":"621e69429877c463542ec213ad4325df50b554d44c1142d9cfece07dae900409"},"id":"c410078d-8013-4beb-adb9-e813b11e5cf3","version":3}' > ~/db_dpos/db1/keystore/UTC--2021-06-29T00-01-57.678362366Z--0d4a5c97aace5d2b60bf0859366450a1a46bc680

# 第2个节点的miner.etherbase为 #0x66B7A2015d74a431D8fcE7cEc738D286D8606FCb
$ mkdir ~/db_dpos/db2/keystore
$ echo '{"address":"66b7a2015d74a431d8fce7cec738d286d8606fcb","crypto":{"cipher":"aes-128-ctr","ciphertext":"5b55e01d746707a6481d1198c8919447574be2953ee8ba3e5ab1643960c822c4","cipherparams":{"iv":"f7c46157ac5a47dcc0a2a0f0cdbd37b9"},"kdf":"scrypt","kdfparams":{"dklen":32,"n":262144,"p":1,"r":8,"salt":"3ab97ad457f5f9af8e94627edcaa9b124bda60b198492237ef55676eb5dd79a1"},"mac":"476b8d1625e6aea30310bd7be3451d1d5a5eddde4e6f8758dcd81caf803dcc7a"},"id":"4aa09332-b2a2-43e1-9bcf-1aca7b9021f3","version":3}' > ~/db_dpos/db1/keystore/UTC--2021-06-29T00-03-12.616513616Z--66b7a2015d74a431d8fce7cec738d286d8606fcb

# 第3个节点的miner.etherbase为 #0x92050446bfeFD2D821b6d6D5f31ECe1E1B3958c2 (虽然轻节点是不能挖矿的，但这里我还是给它一个账户）
$ mkdir ~/db_dpos/db3/keystore
$ echo '{"address":"92050446bfefd2d821b6d6d5f31ece1e1b3958c2","crypto":{"cipher":"aes-128-ctr","ciphertext":"e85aaf50f2af2cc20db0f0adcd3cc2c2054caf3cfdecd08ff9b88d4415d4bae8","cipherparams":{"iv":"1260082d6af5f013b7b688de347cd787"},"kdf":"scrypt","kdfparams":{"dklen":32,"n":262144,"p":1,"r":8,"salt":"c4403bcbeb86c9c546f2f7b59d5be22457230f0a40e669225477b8c887f9f286"},"mac":"a117301a5ed43f2a061f4660c91205d1f90b398001b9aa5a95446ad662879b1a"},"id":"14d65321-c0f1-4988-8aa8-9aac7fa098bc","version":3}' > ~/db_dpos/db1/keystore/UTC--2021-06-29T00-02-50.943664681Z--92050446bfefd2d821b6d6d5f31ece1e1b3958c2
 
# 当然你也可以为每个数据库生成新帐号
~/dpos/build/bin/geth account new  --datadir ~/db_dpos/db1 
~/dpos/build/bin/geth account new  --datadir ~/db_dpos/db3
~/dpos/build/bin/geth account new  --datadir ~/db_dpos/db2 
```
6. 创建一个genesis.json文件,用来初始化创世块。
```sh
echo '{
  "config": {
    "chainId": 6181,
    "homesteadBlock": 0,
    "eip150Block": 0,
    "eip150Hash": "0x0000000000000000000000000000000000000000000000000000000000000000",
    "eip155Block": 0,
    "eip158Block": 0,
    "byzantiumBlock": 0,
    "constantinopleBlock": 0,
    "petersburgBlock": 0,
    "istanbulBlock": 0,
    "dpos": {
      "slotInterval": 2,
      "epochInterval": 30
    }
  },
  "nonce": "0x0",
  "timestamp": "0x60c95d44",
  "extraData": "0x4100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000003C0D4A5c97AACe5D2B60Bf0859366450a1A46BC68066B7A2015d74a431D8fcE7cEc738D286D8606FCb92050446bfeFD2D821b6d6D5f31ECe1E1B3958c22001FF0000000000000000000000000000000000000000000000000000000000004B180D4A5c97AACe5D2B60Bf0859366450a1A46BC6803f8000001866B7A2015d74a431D8fcE7cEc738D286D8606FCb3f8000001892050446bfeFD2D821b6d6D5f31ECe1E1B3958c23f800000",
  "gasLimit": "0x47b760",
  "difficulty": "0x1",
  "mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
  "coinbase": "0x0000000000000000000000000000000000000000",
  "alloc": {
    "0D4A5c97AACe5D2B60Bf0859366450a1A46BC680": {
      "balance": "0x56BC75E2D63100000"
    },
	"92050446bfeFD2D821b6d6D5f31ECe1E1B3958c2": {
      "balance": "0x56BC75E2D63100000"
    },
	"66B7A2015d74a431D8fcE7cEc738D286D8606FCb": {
      "balance": "0x56BC75E2D63100000"
    }
  },
  "number": "0x0",
  "gasUsed": "0x0",
  "parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000"
}' > ~/db_dpos/genesis.json
```
7. 初始化创世块
```sh
$ ~/dpos/build/bin/geth --datadir ~/db_dpos/db1 init ~/db_dpos/genesis.json
$ ~/dpos/build/bin/geth --datadir ~/db_dpos/db2 init ~/db_dpos/genesis.json
$ ~/dpos/build/bin/geth --datadir ~/db_dpos/db3 init ~/db_dpos/genesis.json
```
8. 我了方便测试，在每个db文件夹创建各自的static-nodes.json
```sh
echo '["enode://9da6efb2edf2e77b5f66d147ef6698dab5edfcb84fbe4e31763485a5d6d9195c2ec7e6a9c864b0f0a1b018e954f357c7b15b7111eb48638d5a241a640fa0b728@127.0.0.1:30304","enode://f83ea46ee46d388e2cc6844a05b50a0cf10c14161e7b596287ab34f7e7e47b078724a0d403e2140823a90d4461236cfd431813ec18ff4908764e045b3245f834@127.0.0.1:30305"]' > ~/db_dpos/db1/static-nodes.json

echo '["enode://43d00ec8058eb378bce76686904e21095b170174bd688f0bee3d548cca9eaa9b9d3326c3f3c84c70540be8fd55bbead2a59e384a5a3133994156aeffe589312d@127.0.0.1:30303","enode://f83ea46ee46d388e2cc6844a05b50a0cf10c14161e7b596287ab34f7e7e47b078724a0d403e2140823a90d4461236cfd431813ec18ff4908764e045b3245f834@127.0.0.1:30305"]' > ~/db_dpos/db2/static-nodes.json

echo '["enode://43d00ec8058eb378bce76686904e21095b170174bd688f0bee3d548cca9eaa9b9d3326c3f3c84c70540be8fd55bbead2a59e384a5a3133994156aeffe589312d@127.0.0.1:30303","enode://9da6efb2edf2e77b5f66d147ef6698dab5edfcb84fbe4e31763485a5d6d9195c2ec7e6a9c864b0f0a1b018e954f357c7b15b7111eb48638d5a241a640fa0b728@127.0.0.1:30304"]' > ~/db_dpos/db3/static-nodes.json

```

9. 开启节点前，创建一个password文件就为了方便测试，不需要一直手动unlock账号。
```sh
$ echo 'abc123' > ~/db_dpos/password.txt
```

10. 开启各别的节点并进入console
```sh
# node1 (mode=full)
$ ~/dpos/build/bin/geth --networkid 6181 --identity "node1" --miner.threads=1 --datadir ~/db_dpos/db1   --miner.etherbase=0x0D4A5c97AACe5D2B60Bf0859366450a1A46BC680 --http --http.port 8545 --http.addr 0.0.0.0 --http.corsdomain "*" --http.api "eth,web3,debug,personal,dpos,les" --allow-insecure-unlock --unlock 0x0D4A5c97AACe5D2B60Bf0859366450a1A46BC680 --port 30303 --syncmode "full" --password ~/db_dpos/password.txt --light.serve 20 --nodiscover --nodekeyhex e92e41af95eb6f0425f8e7f1d31dac1446075aafbf342c8a4ce09b0651a4f5df console

# node2 (mode=fast)
$ ~/dpos/build/bin/geth --networkid 6181 --identity "node2" --miner.threads=1 --datadir ~/db_dpos/db2   --miner.etherbase=0x66B7A2015d74a431D8fcE7cEc738D286D8606FCb --http --http.port 8546 --http.addr 0.0.0.0 --http.corsdomain "*" --http.api "eth,web3,debug,personal,dpos" --allow-insecure-unlock --unlock 0x66B7A2015d74a431D8fcE7cEc738D286D8606FCb --port 30304 --syncmode "fast" --password ~/db_dpos/password.txt --light.serve 20 --nodiscover --nodekeyhex 2f9e4a932c0b7779aa019191467ec57322ab5d26e27052af94a506c91cf30a0c console

# node3 (mode=light)
$ ~/dpos/build/bin/geth --networkid 6181 --identity "node3" --miner.threads=1 --datadir ~/db_dpos/db3   --miner.etherbase=0x92050446bfeFD2D821b6d6D5f31ECe1E1B3958c2 --http --http.port 8547 --http.addr 0.0.0.0 --http.corsdomain "*" --http.api "eth,web3,debug,personal,dpos,les" --allow-insecure-unlock --unlock 0x92050446bfeFD2D821b6d6D5f31ECe1E1B3958c2 --port 30305 --syncmode "light" --password ~/db_dpos/password.txt --nodiscover --nodekeyhex 7caf0ac347af4f6faeb98c275c0cd14af74e13122e10209787dd16fa78da6f6a console
```

11. 挖矿(只限node1和node2)
```sh
# node1 console
> miner.start()

# node2 console
> miner.start()
```
这时node2会显示错误:fast syncing, discarded propagated block (其实作者也指明这是一个问题 [GETH1.9.25源码](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/eth/handler.go#L182))。这是因为fast sync模式下会直接丢弃广播的块,只有在进入full sync之后才会接收。解决方法是重启node2，同步后便自动切换成full sync。到此，node1 和node2会轮流出块并被对方同步。

12. 但其实我们并没有测试到fast sync的重点。fast sync会将一串接收的blocks切分成3个部分beforeP, P 和 afterP （p表示pivot) [GETH1.9.25源码](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/eth/downloader/downloader.go#L1795)。 beforeP是没有state的，P开始同步state,而afterP会调用blockchain.insertchain(...)走回正常的链插入流程。
```sh
# 如果同步的区块高度超过了 64, 删除node2的数据库
$ ~/dpos/build/bin/geth removedb --datadir ~/db_dpos/db2

# 重新载入创世块
$ ~/dpos/build/bin/geth --datadir ~/db_dpos/db2 init ~/db_dpos/genesis.json

```
然后再重启node 2,这时fast sync会完成同步区块并关闭fast sync。如果node2显示错误:Node data write error, err="trie node 1ee58d…cb7699 failed with all peers (1 tries, 1 peers)",说明Pivot这个高度的块的state在node1已经不存在了！原因是对方节点只保留着最新的128个块的state在内存，当节点被中止时，只有位置在0,1和127的块的state会被写入数据库 [GETH1.9.25源码](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/core/blockchain.go#L1026)。

想要完成同步，那只有removedb & int genesis, 再开启node2成full sync。

> ❗ 如果曾经开启过fast sync，那么节点的sync模式会在不同条件触发fast/full sync间的切换 [GETH1.9.25源码 1](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/eth/handler.go#L119) | [2](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/eth/sync.go#L273)

13. 轻节点同步

> ❗ 不同于全节点,轻节点只同步header。如果存在trusted checkpoint, 那么同步时从checkpoint块开始，届时最老的块将没有父块。

几个奇怪的坑你可能会遇上，但同步还是可以完成。包括：

* 首先一开始同步时，就遇到了对方节点抛出错误:Light server not synced, rejecting peer [GETH1.9.25源码](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/les/server_handler.go#L164), 然后就把链接断开了。这不应该，因为对方节点确定已同步完成。在dpos版本，我把检查是否同步拿掉后，链接并同步成功。
* 假设node1或node2已开启，但开启轻节点node3时，很大可能会遇到panic: state machine not started yet。触发原因还不详，但只要重启几次就能成功。

14. 轻节点checkpoint同步

由于params.CHTFrequency的默认值是32768，这造成测试很困难，建议把这些参数都除以256 [GETH1.9.25源码, L25-L54](https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/params/network_params.go)。至此，每达到params.CHTFrequency-1的倍数的块时LES都会在本地生成CHT/BloomTrie root。

接下去，你还需要把最新的CHT/BloomTrie root硬写入TrustedCheckpoints map [DPOS源码](https://github.com/atlaschiew/ethereum-clique-to-dpos/blob/main/params/config.go)。

一旦新轻节点客户端接入，它会从checkpoint之前最近的epoch块 [DPOS源码](https://github.com/atlaschiew/ethereum-clique-to-dpos/blob/d12fe21ac581da30c0565234178c818bd718e5f3/light/lightchain.go#L511) 开始同步。完成后,这个epoch块的父块将不存在于本地,造就断链的状况，所以设计共识引擎时务必要注意这个。

> ✅ 总结来说，三种节点间的同步都能运行顺利。

## 源码分析

### 项目结构
```sh
$ cd path_to_geth/consensus/dpos 

action.go    #关于可以通过TX写入DPOS相关的方法，例如becomeCandidate,becomeDelegator,quitCandidate和quitDelegator
api.go       #可以通过js console访问的API类
dpos.go      #DPOS的核心，主要实现consensus.Engine接口
main_test.go #测试文件 
proposal.go  #关于提案相关的方法，目前只有dpos.Proposals.TestProposal一个
snapshot.go  #快照,避免对链进行投票统计时造成性能耗损
utils.go     #常用函数
```
### 出块流程
这里先陈列和出块相关的consensus.Engine的接口定义，然后就是重定义哪些header字段，最后才讲相关的接口函数的内容和流程。

#### consensus.Engine接口定义: 
```
type Engine interface {
	
    	...
    	
	/*
	在worker.commitNewWork()里被调用，此函数用来来初始化新块的头部。如果是epoch块，成功中选的签名者/委托人/提案结果都会储存在header.Extra。
	*/
	Prepare(chain ChainHeaderReader, header *types.Header) error
	
	/*
	从入参的state就知道这里可以让共识引擎改变state。在DPOS,这里处理签名者和委托人的奖励，并把state root更新在header.Root。
	*/
	Finalize(chain ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
		uncles []*types.Header)

	/*
	和Finalize(...)一样，但这个返回新块。被worker.commitNewWork()调用
	*/
	FinalizeAndAssemble(chain ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
		uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error)

	/*
	签名并把最终的块写入results通道，接收results通道一方将新块和state一并写入数据库。
	*/
	Seal(chain ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error

	...
}
```
#### header字段的重定义: 
1. header.MixDigest (common.Hash类) 用来记录签名者想投的提案。
2. header.Coinbase永远是空，因为与clique不同，候选人不再由签名者提拔。
3. header.Extra格式不同,改成像bitcoin tx的编码风格，有varint的概念。

epoch块的header.Extra (以下取自genesis.json, 创世块也是epoch块)
```sh
0x4100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000003C0D4A5c97AACe5D2B60Bf0859366450a1A46BC68066B7A2015d74a431D8fcE7cEc738D286D8606FCb92050446bfeFD2D821b6d6D5f31ECe1E1B3958c22001FF0000000000000000000000000000000000000000000000000000000000004B180D4A5c97AACe5D2B60Bf0859366450a1A46BC6803f8000001866B7A2015d74a431D8fcE7cEc738D286D8606FCb3f8000001892050446bfeFD2D821b6d6D5f31ECe1E1B3958c23f800000

#四种元素，他们是签名、多签名者、提案结果和多委托人对应多签名者。41,3C,20,4B均为数据块长度，而4B的数据块又产生三个子数据块，长度均为18。
41 #签名
	0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000
3C #多签名者
	0D4A5c97AACe5D2B60Bf0859366450a1A46BC680 66B7A2015d74a431D8fcE7cEc738D286D8606FCb 92050446bfeFD2D821b6d6D5f31ECe1E1B3958c2 
20 #提案结果
	01FF000000000000000000000000000000000000000000000000000000000000
4B #多委托人对应多签名者
	18 #多委托人对应第一个签名者, 和其占据的%份额 (32 bits float), 3f800000表示 1或100%
		0D4A5c97AACe5D2B60Bf0859366450a1A46BC680 3f800000
	18 #多委托人对应第二个签名者
		66B7A2015d74a431D8fcE7cEc738D286D8606FCb 3f800000
	18 #多委托人对应第三个签名者
		92050446bfeFD2D821b6d6D5f31ECe1E1B3958c2 3f800000
		
```
非epoch块的header.Extra 
```sh
0x410000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000

#只有签名一种元素，41为该数据块的长度
41 #签名
	0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000
	
```
#### 接口函数的内容和流程: 

出块流程是, engine.Prepare(...) -> engine.FinalizeAndAssemble(...) -> engine.Seal(...)。这些函数都由miner.worker调用。以下分别对各个函数做简单说明，具体的说明已写入源码。

`Prepare()`, 初始化新块。

`FinalizeAndAssemble(...)`, 主要做两样东西，1) 创造header.extra字段 2) 颁发奖励。由于snap依赖于state而FinalizeAndAssemble又入参statedb,那么任何需要用到snap对象的工作都只好到这里发生。

这里重点在出epoch块的流程。当程序读取epochInterval-1的snap的字段(PreElectedSigners, PreElectedDelegators, UnconfirmedProposals)后才把这些信息写入新块的extra。这三个字段的用意分别为:
1. PreElectedSigners：达标的前签名者会继续留在候选人列表，然后开启竞争，依委托人的余额选出他们支持的21位签名者，余额越多表示支持率越高，
2. PreElectedDelegators：记录中选的委托人，他们支持的签名者对象必须出现在PreElectedSigners
3. UnconfirmedProposals: 记录提案结果，同一个提案(proposal)可以做多个不同值的子提案，最后支持率最高的子提案才能被定案。如果出现两个最多支持率的子提案，那么提案将不做出任何改变。

另一个重点便是奖励分发。奖励是由签名者和支持他的委托人共同获得，比例按dpos.signerReward来分配出签名者和多委托人能获得的份额。然后每位委托人还要依据他们所投的份额再稀释成最终能获得的数额。

`Seal()`, 重点在于签名,和clique一样，签名者的地址不直接存在任何header字段，调用ecrecover(...)便可获得。另外，这里还做了最后的两项检查, 1) 自己是否是合格的签名者, 2) 签名者是否在signer limit个区块里多出一次块。

### 验证流程
这里先陈列和验证相关的consensus.Engine的接口定义，然后才讲相关的接口函数的内容和流程。

#### consensus.Engine接口定义: 
```
type Engine interface {
	
    	...
	
    	/*
	验证区块头。在fetcher中被使用，当fetcher要插入广播来的区块时，需要先对区块头进行校验。
	*/
	VerifyHeader(chain ChainHeaderReader, header *types.Header, seal bool) error

	/*
	被 1）BlockChain.insertChain，2）LightChain.InsertHeaderChain 调用，这方法是对多个块头做验证，而每个块头的验证其实是调用VerifyHeader()来完成。
	*/	
	VerifyHeaders(chain ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error)

	/*
	验证区块中的叔块。在BlockChain.insertChain里，调用VerifyHeaders之后，才对叔块进行验证。
	
	https://github.com/ethereum/go-ethereum/blob/e7872729012a4871397307b12cc3f4772ffcbec6/core/blockchain.go#L1739
	block, err := it.next()
    	-> ValidateBody
        	-> VerifyUncles
	*/
	VerifyUncles(chain ChainReader, block *types.Block) error

	/*
	验证Seal()所修改的区块头字段,和seal()一样这里也需要用到snap做验证。
	
	和clique不同，dpos将VerifySeal从verifyheader里移走，然后在blockchain.insertChain(...)里硬写入。原因是verifyseal依赖于state和body
	
	轻节点不会触发verifyseal,因为它本身没有body和state,所以无法创建snap来做验证。	
	*/
	VerifySeal(chain ChainHeaderReader, header *types.Header) error
	
	/*
	在StateProcessor.Process()里，执行完全部交易后被调用，目的就是要取得最新的state root和新接收的块做验证比较。
	*/
	Finalize(chain ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
		uncles []*types.Header)
		
	...
}
```
#### 接口函数的内容和流程: 
关于新块的插入，无论是被动接收(fetcher)或主动接收(downloader),最后都是调用blockchain.insertChain(...)或lightchain.InsertHeaderChain(...)实现入库入链。

`blockchain.insertChain(...)`。验证DPOS块的基本流程是 engine.VerifyHeaders(...) -> engine.VerifyHeader(...) -> BlockValidator.ValidateBody() -> engine.VerifyUncles() -> BlockValidator.ValidateState -> engine.VerifySeal(...)。

其中engine.VerifyUncles()收藏于BlockValidator.ValidateBody()。和clique不同的是 engine.VerifySeal(...)不再收藏于engine.VerifyHeader(...),而是发生在BlockValidator.ValidateState之后直接调用，因为VerifySeal里的snap必须用到入库的block.body和state对象，所以才不得已而为之。在snapshot篇，这部分会再说说。

`lightchain.InsertHeaderChain(...)`。由于轻节点的块不包括body和state，所以验证DPOS块的基本流程只是 engine.VerifyHeaders(...) -> engine.VerifyHeader(...)。 

### 快照 (snapshot)
调用者可以通过snapshot方法获取不同块高度时的快照状态, 主要两个应用场景 1)出块 和 2) 验块。关于snapshot结构体，读者可以通过源码了解，这里就不再发布。

想获取snapshot,go环境下就调用dpos.snapshot(...)，console环境下则调用dpos.getSnapshot(...)。

整个dpos包就有5处调用dpos.snapshot(...),下面简单介绍，它们分别为
1. `dpos.verifySeal(...)` 验块用途。取新块-1的snap和新块的信息做对比。
2. `dpos.FinalizeAndAssemble(...)` 出块用途。取新块-1的snap并将snap的信息作为新块头的信息。
3. `dpos.Seal(...)` snap的作为是验证自己（签名者）是否合格。同时，如果这轮自己没有优先权，那么延迟wiggleTime之久才发布新块。
4. `dpos.CalcDifficulty(...)` 这方法在FinalizeAndAssemble(...)里被调用并填充header.Difficulty, 如果自己有优先权，那么值便是2否则为1。
5. `dpos.Api.GetSnapshot(...)` console环境下调用dpos.getSnapshot(number)便能读取某个区块高度的snap。如果number是表示这值是最高的块。

现在来说说dpos.snapshot(...)方法是如何产生snap, 主要第一个for循环是为了找最近的snap,for exit后就到snapshot.apply统计从这个snap的块到想寻找的块。

<p align="center">
<img src="https://user-images.githubusercontent.com/45816141/128964716-905d85a3-30f0-4934-9fbf-5b07377d6a0d.PNG"/><br/>
读取某个块的snap流程
</p>

由于`snapshot.apply`有可能会统计过多的块而造成性能问题，所以解决方法是持久化储存snap但必须达到这三种条件的任何一种，它们分别为 1) 创世块, 2) EpochInterval-1整数倍的块和 3) storeSnapInterval整数倍的块。

这三个特殊的snap字段PreElectedSigners/PreElectedDelegators/UnconfirmedProposals只发生在EpochInterval-1整数倍的块，所以当出新epoch块时，就可以用它们填充新块的extra字段。和clique不同的是只要达到半数票，签名者就可以立即被加入或踢出,在dpos这是要等到epoch块才决定的。

当`snapshot.apply`处理到epoch块时，就会处理以下的事情
1. 处理被踢者, 被踢出者将丧失候选人身份，除此之外投他人或自己被他人投的票也变成无效。
2. 这三个和选举结果相关的字段PreElectedSigners/PreElectedDelegators/UnconfirmedProposals将转正（复制到其他字段)并被清空。
3. 清空Votes/Tally 这两个投提案的字段

最后，snapshot的这两个字段Candidates和Delegators是会一直累计的，这终究会造成臃肿问题。

### 选举

在dpos有两项选举，a）选出新签名者，b）通过新提案。前者是由广大群众投票选出，后者是签名者投票选出。

#### a) 选出新签名者
任何人都可以投选自己心目中的候选人，最后以票重选出最高支持率的dpos.maxSignerSize个签名者。

以下解释各个角色和他们之间的关系：
1. 签名者(signer)，表示合法的出块人,签名者也一定是候选人,这记录在snapshot.ElectedSigners
2. 候选人(candidate),可以通过becomeCandidate TX自荐，不合格的签名者会从snapshot.candidate/snapshot.delegator里移除。这记录在snapshot.Candidates。
3. 委托人(delegator),或称选民，可以通过becomeDelegator TX投给心目中的候选人。一个sender地址只能投给一个人。这记录在snapshot.Delegators。
4. 被踢出者(kickout signer), 在任签名者时由于出块任务没有达标而丧失成为签名者和候选人，这也导致投他的委托人也被取消资格。

以下这4种特殊的tx都和角色操作有关并记录在consensus/dpos/action.php，它们分别为：
1. `becomeCandidate` 成为候选人
2. `becomeDelegator` 成为委托人
3. `quitCandidate` 取消成为候选人
4. `quitDelegator` 取消成为委托人

触发它们的方法是把想要的action对象编成bytes并写入tx.data (txdata.Payload)，然后发送tx到0x0000000000000000000000000000000000000001这个特殊的地址。当snapshot.apply(...)取得block.Body().Transactions就会处理这4种特殊的tx。

选新签名者的过程，以下的变量都在snapshot.apply(...)
1. `minMintTarget` 表示最低需要达到的出块数，否则当前签名者将被提出。
2. `candidateCnt` 表示可用候选人。
3. 根据出块数从少到多排序当前多签名者，如果还有可用候选人AND不达标的签名者放入`kickoutSigners`里, 否则放入`candidateVotes`里。candidateVotes里的人表示有资格可以竞选成新签名者。
4. `candidateVotes[candidate].Add(candidateVotes[candidate],balance)` 累计每个候选人的得票。得票的概念其实是依据委托人的balance。假设一名候选人只有一名委托人并且该名委托人的balance是个大数目，相较于另一名候选人有多名委托人，但累计起来的balance只是个小数目，那么结果是前者更占优势。
5. 根据得票比重从多到少排序候选人，并取出前面dpos.maxSignerSize个候选人成为下一轮的签名者。
6. 新签名者和其对应的委托人都会写在epoch块的extra。

#### b) 通过新提案
投票新提案和投票候选人不同的是这只是签名者能投而已。目前默认的提案都写在 consensus/dpos/proposal.go且只有一个，作为测试用途。

成立新提案的过程
1. 在console,签名者可以通过dpos.API.Propose(...)提交提案到本地或dpos.API.disgard(...)删除提案。
2. 签名者投票方式在于出块。每次出块是都会随机从之前propose的列表中获取一项并记录在header.MixDigest。
3. 赞成票header.Nonce=0xffffffffffffffff，投取消赞成票header.Nonce=0x0000000000000000。注意这里没有反对票，不投就意味着是反对票。如果投了赞成票想取消，那么就投取消赞成票。
4. 同一个提案，可以有多个子提案，但最终一个提案只有一个子提案胜出。如果同时两个子提案的获票率相等，那么这个提案将不做任何改变。
5. 最终各个提案值都会写在epoch块的extra。

### API
以太坊rpc服务器提供三种连接方法：HTTP、websocket和IPC来调用API。

dpos的RPC API都收录在consensus/dpos/api.go, 它们的功能分别为:
1. `GetSnapshot` 取某个块的高度的snap,如果入参的块高度为空，那么块高度就是最新的块。
2. `GetSnapshotAtHash`取入参块哈希的snap。
3. `Proposals` 取自己propose过的记录。
4. `Propose` 添加子提案，value为32字节的子提案, yesNo: yes | no， yes表示赞成票,no则表示取消赞成票。
5. `Discard` 从proposals列表里删除子提案。

以上只是dpos.API对象的方法，外部依然无法调用，这时我们需要实现consensus接口里的dpos.APIs(...)，那么程序才有办法把dpos api注册到rpc server。
#### consensus.Engine接口定义: 
```
type Engine interface {
    	...
	
	APIs(chain ChainHeaderReader) []rpc.API
    	
	...
}
```

dpos.APIs(...)方法返回一组rpc api,但事实上只有一个rpc.API类的元素: `{Namespace:"dpos", Version:"1.0", Service:&API{chain: chain, dpos:self}, Public:false}`, 其中service字段最核心，因为记录了dpos.API对象。另外 public:false 表示如果节点没有加上 --http.api "eth,web3,debug,personal,dpos,les" 就启动, 那么这个API是无法被外部调用的。

程序通过`rpc.server.RegisterName`注册这些API后 `rpc.Server.serveRequest` 才能对发自客户端的请求进行解析并调用相应的注册过的 API。

最后一步，我们还要在console客户端添加相关的js用来调用这些dpos API。两个地方可以添加 1) internal/web3ext/web3ext.go 或直接注入进 2) internal/jsre/deps/web3.js。和clique一样，我选择了第一种方法，打开文件便能找到 `DposJs`。

## 后语
由于本人还是golang和geth的新手，故优化方面并没有做足，目标只是让程序能跑起来。对于geth，还有很多方面不清楚，所以我的学习途径便是从共识引擎开始，再逐步向外扩展。
