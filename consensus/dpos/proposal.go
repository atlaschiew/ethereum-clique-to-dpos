/*
dpos自带的提案功能，目前只有一个提案，用作测试用途，名为TestProposal#1

在一个周期内(epoch)相同的提案不能被重复
*/
package dpos

import(
	"github.com/ethereum/go-ethereum/common"
	"encoding/binary"
	"errors"
	"bytes"
	_ "fmt"
)

const (
	TestProposal uint8 = iota + 1	
)

type Proposal struct {
	Id          uint8
	Values 		[]interface{}
	Description string
	ValidateValuesFn  func(uint8, []interface{}) (error)
	ValidateBytesFn func(common.Hash) (error)
	ToBytesFn func([]interface{}) ([]byte)
	FromBytesFn func(common.Hash) ([]interface{})
}

var Proposals map[uint8]*Proposal = map[uint8]*Proposal{
	TestProposal: &Proposal{
		Id          : TestProposal,
		Values      : make([]interface{},0),
		Description : "This is test proposal by dpos",
		
		ValidateValuesFn	: func(id uint8, values []interface{}) (error) {
			value := values[0].(uint8)
			
			if !(value > 0 && value <= 255) {
				return errors.New("Invalid proposal#" + string(id))
			}
			
			return nil
		},
		
		ValidateBytesFn: func(_bytes common.Hash) (error) {
			value := _bytes[1]
			
			if !(value > 0 && value <= 255) {
				return errors.New("Invalid proposal#" + string(_bytes[0]))
			}
			
			if !bytes.Equal(_bytes[2:], bytes.Repeat([]byte{0x00}, common.HashLength-2)) {
				return errors.New("Invalid proposal#" + string(_bytes[0]))
			}
			
			return nil
		},
		
		ToBytesFn : func(values []interface{}) ([]byte) {
			value := values[0].(uint8)
			
			buf := new(bytes.Buffer)
			binary.Write(buf, binary.BigEndian, value)
	
			return buf.Bytes()
		},
		
		FromBytesFn: func(bytes common.Hash) ([]interface{}) {
			return []interface{}{bytes[1]}
		},

	},
}

func getProposal(id uint8) (*Proposal,error) {
	proposal, ok := Proposals[id]
	
	if ok {
		return &(*proposal),nil //new
	} else {
		return &Proposal{}, errors.New("Proposal not found")
	}
}

/*
编码的值代表proposal信息并记录在block.header.mixdigest
*/
func(self *Proposal) toBytes() (common.Hash,error) {
	
	if err:=self.ValidateValuesFn(self.Id, self.Values);err!=nil {
		return common.Hash{}, err
	}
		
	result := []byte{uint8(self.Id)}
	result = append(result, self.ToBytesFn(self.Values)...)
	result = append(result, bytes.Repeat([]byte{0x00}, common.HashLength-len(result))...)
	
	return common.BytesToHash(result),nil
}

/*
解码block.header.mixdigest里的值成proposal对象
*/
func(self *Proposal) fromBytes(proposalBytes common.Hash) error {
	
	id := proposalBytes[0]	
	
	proposal, err := getProposal(id)
	
	if err == nil {
		
		self.ValidateBytesFn  = proposal.ValidateBytesFn
		
		if err:=self.ValidateBytesFn(proposalBytes);err!=nil {
			return err
		}
		
		self.Id          = proposal.Id
		self.Description = proposal.Description
		self.ValidateValuesFn  = proposal.ValidateValuesFn
		self.ToBytesFn   = proposal.ToBytesFn
		self.FromBytesFn = proposal.FromBytesFn
		self.Values      = self.FromBytesFn(proposalBytes)
		
	} else {
		return errors.New("Proposal not found")
	}
	
	return nil
}

