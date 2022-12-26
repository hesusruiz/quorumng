// newnodekey generates a new nodekey to be used in Ethereum-compatible networks
package main

import (
	"fmt"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/crypto"
)

func main() {

	nodeKey, err := crypto.GenerateKey()
	if err != nil {
		utils.Fatalf("could not generate key: %v", err)
	}
	fmt.Printf("nodekey: %x\n", crypto.FromECDSA(nodeKey))
	fmt.Printf("enode: %x\n", crypto.FromECDSAPub(&nodeKey.PublicKey)[1:])

}
