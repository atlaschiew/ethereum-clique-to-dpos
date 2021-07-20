package dpos

import(
	"testing"
	"encoding/hex"
	"bytes"
	"github.com/ethereum/go-ethereum/common"
)

func toBytes(hexStr string) []byte {
	decoded, _ := hex.DecodeString(hexStr)
	
	return decoded
	
}

func TestSortConfirmedSignersAsc(t *testing.T) {
	ConfirmedSigners := make(map[common.Address]uint16, 3)
	ConfirmedSigners[common.HexToAddress("0x0000000000000000000000000000000000000001")] = uint16(3)
	ConfirmedSigners[common.HexToAddress("0x0000000000000000000000000000000000000002")] = uint16(2)
	ConfirmedSigners[common.HexToAddress("0x0000000000000000000000000000000000000003")] = uint16(1)
	
	sorted:= confirmedSignersSorter(ConfirmedSigners)
	
	t.Log("check sorting", sorted)
	
}

func TestSelectedProposals(t *testing.T) {

	groupProposals := make(map[uint8]map[common.Hash]int)
	
	groupProposals[uint8(1)] = make(map[common.Hash]int)
	groupProposals[uint8(1)][ common.Hash{1,5,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0} ] = 3
	groupProposals[uint8(1)][ common.Hash{1,10,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0} ] = 4
	groupProposals[uint8(1)][ common.Hash{1,0xff,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0} ] = 4
	
	selectedProposals := make(map[uint8]common.Hash)
	for proposalId, proposalVotes := range groupProposals {
				
		if len(proposalVotes) > 1 {
			sorted := tallySorter(proposalVotes)
					
			if sorted[0].Value == sorted[1].Value {
				continue
			}
			
			selectedProposals[proposalId] = sorted[0].Key.(common.Hash)
		} else {
			for proposalBytes := range proposalVotes {
						
				selectedProposals[proposalId] = proposalBytes
			}
		}
	}
	
	t.Log(selectedProposals)
}

func TestUnserialize(t *testing.T) {
	var tests =[]struct {
		extra []byte
		want [][]byte
	}{
		{
			toBytes("40000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000003C0D4A5c97AACe5D2B60Bf0859366450a1A46BC68092050446bfeFD2D821b6d6D5f31ECe1E1B3958c266B7A2015d74a431D8fcE7cEc738D286D8606FCb2001FF000000000000000000000000000000000000000000000000000000000000"),
			
			[][]byte{
				toBytes("00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"),
				toBytes("0D4A5c97AACe5D2B60Bf0859366450a1A46BC68092050446bfeFD2D821b6d6D5f31ECe1E1B3958c266B7A2015d74a431D8fcE7cEc738D286D8606FCb"),
				toBytes("01FF000000000000000000000000000000000000000000000000000000000000"),
				
			},
		},
	}
	
	for _, test := range tests {
		datas := unserialize(test.extra)
		
		for i, data := range datas {
			if !bytes.Equal(data, test.want[i]) {
				t.Error("given", data, "expected", test.want[i])
			} else {
				t.Log("yes")
			}
		}
	}
}