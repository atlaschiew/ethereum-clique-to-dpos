<p align="center">
    <h3 align="center">以太坊DPOS</h3>
    <p align="center">
		一个基于CLIQUE共识改编的以太坊DPOS。本章以中文书写。 任何有误的地方，请不吝指教。
    </p> 
</p>

<br/>
<br/>

## 目录

* [DPOS是什么？CLIQUE又是什么？](#what-is)
* [设计思路](#design-rationale)
* [安装部署](#deployment)
* [源码分析](#coding-study)
  * [Initialization](#initialization)
  * [Request Handling](#request-handling)
  * [Block Generation](#block-generation)
  * [Transaction Generation](#transaction-generation)
  * [Fork Handling](#fork-handling)
  * [Address](#address)
 
 
## DPOS是什么？CLIQUE又是什么？

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