// Copyright 2014 The go-ethereum Authors
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

package core

import (
	"errors"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/private"
)

var (
	errInsufficientBalanceForGas = errors.New("insufficient balance to pay for gas")
)

/*
The State Transitioning Model

A state transition is a change made when a transaction is applied to the current world state
The state transitioning model does all the necessary work to work out a valid new state root.

1) Nonce handling
2) Pre pay gas
3) Create a new state object if the recipient is \0*32
4) Value transfer
== If contract creation ==
  4a) Attempt to run transaction data
  4b) If valid, use result as code for the new state object
== end ==
5) Run Script section
6) Derive new state root
*/
type StateTransition struct {
	gp         *GasPool
	msg        Message
	gas        uint64
	gasPrice   *big.Int
	initialGas uint64
	value      *big.Int
	data       []byte
	state      vm.StateDB
	evm        *vm.EVM
}

// Message represents a message sent to a contract.
type Message interface {
	From() common.Address
	//FromFrontier() (common.Address, error)
	To() *common.Address

	GasPrice() *big.Int
	Gas() uint64
	Value() *big.Int

	Nonce() uint64
	CheckNonce() bool
	Data() []byte
}

// PrivateMessage implements a private message
type PrivateMessage interface {
	Message
	IsPrivate() bool
}

// IntrinsicGas computes the 'intrinsic gas' for a message with the given data.
func IntrinsicGas(data []byte, contractCreation, isHomestead bool, isEIP2028 bool) (uint64, error) {
	// Set the starting gas for the raw transaction
	var gas uint64
	if contractCreation && isHomestead {
		gas = params.TxGasContractCreation
	} else {
		gas = params.TxGas
	}
	// Bump the required gas by the amount of transactional data
	if len(data) > 0 {
		// Zero and non-zero bytes are priced differently
		var nz uint64
		for _, byt := range data {
			if byt != 0 {
				nz++
			}
		}
		// Make sure we don't exceed uint64 for all data combinations
		nonZeroGas := params.TxDataNonZeroGasFrontier
		if isEIP2028 {
			nonZeroGas = params.TxDataNonZeroGasEIP2028
		}
		if (math.MaxUint64-gas)/nonZeroGas < nz {
			return 0, vm.ErrOutOfGas
		}
		gas += nz * nonZeroGas

		z := uint64(len(data)) - nz
		if (math.MaxUint64-gas)/params.TxDataZeroGas < z {
			return 0, vm.ErrOutOfGas
		}
		gas += z * params.TxDataZeroGas
	}
	return gas, nil
}

// NewStateTransition initialises and returns a new state transition object.
func NewStateTransition(evm *vm.EVM, msg Message, gp *GasPool) *StateTransition {
	return &StateTransition{
		gp:       gp,
		evm:      evm,
		msg:      msg,
		gasPrice: msg.GasPrice(),
		value:    msg.Value(),
		data:     msg.Data(),
		state:    evm.PublicState(),
	}
}

// ApplyMessage computes the new state by applying the given message
// against the old state within the environment.
//
// ApplyMessage returns the bytes returned by any EVM execution (if it took place),
// the gas used (which includes gas refunds) and an error if it failed. An error always
// indicates a core error meaning that the message would always fail for that particular
// state and would never be accepted within a block.

func ApplyMessage(evm *vm.EVM, msg Message, gp *GasPool) ([]byte, uint64, bool, error) {
	return NewStateTransition(evm, msg, gp).TransitionDb()
}

// to returns the recipient of the message.
func (st *StateTransition) to() common.Address {
	if st.msg == nil || st.msg.To() == nil /* contract creation */ {
		return common.Address{}
	}
	return *st.msg.To()
}

func (st *StateTransition) useGas(amount uint64) error {
	if st.gas < amount {
		return vm.ErrOutOfGas
	}
	st.gas -= amount

	return nil
}

func (st *StateTransition) buyGas() error {
	mgval := new(big.Int).Mul(new(big.Int).SetUint64(st.msg.Gas()), st.gasPrice)
	if st.state.GetBalance(st.msg.From()).Cmp(mgval) < 0 {
		return errInsufficientBalanceForGas
	}
	if err := st.gp.SubGas(st.msg.Gas()); err != nil {
		return err
	}
	st.gas += st.msg.Gas()

	st.initialGas = st.msg.Gas()
	st.state.SubBalance(st.msg.From(), mgval)
	return nil
}

func (st *StateTransition) preCheck() error {
	// Make sure this transaction's nonce is correct.
	if st.msg.CheckNonce() {
		nonce := st.state.GetNonce(st.msg.From())
		if nonce < st.msg.Nonce() {
			return ErrNonceTooHigh
		} else if nonce > st.msg.Nonce() {
			return ErrNonceTooLow
		}
	}
	return st.buyGas()
}

// TransitionDb will transition the state by applying the current message and
// returning the result including the used gas. It returns an error if failed.
// An error indicates a consensus issue.
//
// Quorum:
// 1. Intrinsic gas is calculated based on the encrypted payload hash
//    and NOT the actual private payload
// 2. For private transactions, we only deduct intrinsic gas from the gas pool
//    regardless the current node is party to the transaction or not
func (st *StateTransition) TransitionDb() (ret []byte, usedGas uint64, failed bool, err error) {
	if err = st.preCheck(); err != nil {
		return
	}
	msg := st.msg
	sender := vm.AccountRef(msg.From())
	homestead := st.evm.ChainConfig().IsHomestead(st.evm.BlockNumber)
	istanbul := st.evm.ChainConfig().IsIstanbul(st.evm.BlockNumber)
	contractCreation := msg.To() == nil
	isQuorum := st.evm.ChainConfig().IsQuorum

	var data []byte
	isPrivate := false
	publicState := st.state
	pmh := newPMH(st)
	if msg, ok := msg.(PrivateMessage); ok && isQuorum && msg.IsPrivate() {
		isPrivate = true
		pmh.snapshot = st.evm.StateDB.Snapshot()
		pmh.eph = common.BytesToEncryptedPayloadHash(st.data)
		data, pmh.receivedPrivacyMetadata, err = private.P.Receive(pmh.eph)
		// Increment the public account nonce if:
		// 1. Tx is private and *not* a participant of the group and either call or create
		// 2. Tx is private we are part of the group and is a call
		if err != nil || !contractCreation {
			publicState.SetNonce(sender.Address(), publicState.GetNonce(sender.Address())+1)
		}

		if err != nil {
			return nil, 0, false, nil
		}

		pmh.hasPrivatePayload = data != nil

		if ok, err := pmh.prepare(); !ok {
			return nil, 0, true, err
		}
	} else {
		data = st.data
	}

	// Pay intrinsic gas. For a private contract this is done using the public hash passed in,
	// not the private data retrieved above. This is because we need any (participant) validator
	// node to get the same result as a (non-participant) minter node, to avoid out-of-gas issues.
	gas, err := IntrinsicGas(st.data, contractCreation, homestead, istanbul)
	if err != nil {
		return nil, 0, false, err
	}
	if err = st.useGas(gas); err != nil {
		return nil, 0, false, err
	}

	var (
		leftoverGas uint64
		evm         = st.evm
		// vm errors do not effect consensus and are therefor
		// not assigned to err, except for insufficient balance
		// error.
		vmerr error
	)
	if contractCreation {
		ret, _, leftoverGas, vmerr = evm.Create(sender, data, st.gas, st.value)
	} else {
		// Increment the account nonce only if the transaction isn't private.
		// If the transaction is private it has already been incremented on
		// the public state.
		if !isPrivate {
			publicState.SetNonce(msg.From(), publicState.GetNonce(sender.Address())+1)
		}
		var to common.Address
		if isQuorum {
			to = *st.msg.To()
		} else {
			to = st.to()
		}
		//if input is empty for the smart contract call, return
		if len(data) == 0 && isPrivate {
			st.refundGas()
			st.state.AddBalance(st.evm.Coinbase, new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), st.gasPrice))
			return nil, 0, false, nil
		}

		ret, leftoverGas, vmerr = evm.Call(sender, to, data, st.gas, st.value)
	}
	if vmerr != nil {
		log.Info("VM returned with error", "err", vmerr)
		// The only possible consensus-error would be if there wasn't
		// sufficient balance to make the transfer happen. The first
		// balance transfer may never fail.
		if vmerr == vm.ErrInsufficientBalance {
			return nil, 0, false, vmerr
		}
	}

	// Quorum - Privacy Enhancements
	// perform privacy enhancements checks
	if pmh.mustVerify() {
		exitEarly := false
		exitEarly, err = pmh.verify(vmerr)
		if exitEarly {
			return nil, 0, true, err
		}
	}
	// End Quorum - Privacy Enhancements

	// Pay gas used during contract creation or execution (st.gas tracks remaining gas)
	// However, if private contract then we don't want to do this else we can get
	// a mismatch between a (non-participant) minter and (participant) validator,
	// which can cause a 'BAD BLOCK' crash.
	if !isPrivate {
		st.gas = leftoverGas
	}

	st.refundGas()
	st.state.AddBalance(st.evm.Coinbase, new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), st.gasPrice))

	if isPrivate {
		return ret, 0, vmerr != nil, err
	}
	return ret, st.gasUsed(), vmerr != nil, err
}

func (st *StateTransition) refundGas() {
	// Apply refund counter, capped to half of the used gas.
	refund := st.gasUsed() / 2
	if refund > st.state.GetRefund() {
		refund = st.state.GetRefund()
	}
	st.gas += refund

	// Return ETH for remaining gas, exchanged at the original rate.
	remaining := new(big.Int).Mul(new(big.Int).SetUint64(st.gas), st.gasPrice)
	st.state.AddBalance(st.msg.From(), remaining)

	// Also return remaining gas to the block gas counter so it is
	// available for the next transaction.
	st.gp.AddGas(st.gas)
}

// gasUsed returns the amount of gas used up by the state transition.
func (st *StateTransition) gasUsed() uint64 {
	return st.initialGas - st.gas
}

// Quorum - Privacy Enhancements - implement the pmcStateTransitionAPI interface
func (st *StateTransition) SetTxPrivacyMetadata(pm *types.PrivacyMetadata) {
	st.evm.SetTxPrivacyMetadata(pm)
}
func (st *StateTransition) IsPrivacyEnhancementsEnabled() bool {
	return st.evm.ChainConfig().IsPrivacyEnhancementsEnabled(st.evm.BlockNumber)
}
func (st *StateTransition) RevertToSnapshot(snapshot int) {
	st.evm.StateDB.RevertToSnapshot(snapshot)
}
func (st *StateTransition) GetStatePrivacyMetadata(addr common.Address) (*state.PrivacyMetadata, error) {
	return st.evm.StateDB.GetStatePrivacyMetadata(addr)
}
func (st *StateTransition) CalculateMerkleRoot() (common.Hash, error) {
	return st.evm.CalculateMerkleRoot()
}
func (st *StateTransition) AffectedContracts() []common.Address {
	return st.evm.AffectedContracts()
}

// End Quorum - Privacy Enhancements
