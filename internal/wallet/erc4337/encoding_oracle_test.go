package erc4337

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// These ABI encoders independently check the manual word encoding in hash.go.
var (
	typeAddress, _ = abi.NewType("address", "", nil)
	typeUint256, _ = abi.NewType("uint256", "", nil)
	typeBytes32, _ = abi.NewType("bytes32", "", nil)
)

// userOpV06HashArgs 用於 v0.6 UserOperation 內部打包 (320 位元組)
var userOpV06HashArgs = abi.Arguments{
	{Type: typeAddress}, // sender
	{Type: typeUint256}, // nonce
	{Type: typeBytes32}, // keccak256(initCode)
	{Type: typeBytes32}, // keccak256(callData)
	{Type: typeUint256}, // callGasLimit
	{Type: typeUint256}, // verificationGasLimit
	{Type: typeUint256}, // preVerificationGas
	{Type: typeUint256}, // maxFeePerGas
	{Type: typeUint256}, // maxPriorityFeePerGas
	{Type: typeBytes32}, // keccak256(paymasterAndData)
}

// userOpV07HashArgs 用於 v0.7 PackedUserOperation 內部打包 (256 位元組)
var userOpV07HashArgs = abi.Arguments{
	{Type: typeAddress}, // sender
	{Type: typeUint256}, // nonce
	{Type: typeBytes32}, // keccak256(initCode)
	{Type: typeBytes32}, // keccak256(callData)
	{Type: typeBytes32}, // accountGasLimits
	{Type: typeUint256}, // preVerificationGas
	{Type: typeBytes32}, // gasFees
	{Type: typeBytes32}, // keccak256(paymasterAndData)
}

// userOpOuterHashArgs 用於外層最終 UserOpHash 計算 (96 位元組)
var userOpOuterHashArgs = abi.Arguments{
	{Type: typeBytes32}, // innerHash
	{Type: typeAddress}, // entryPoint
	{Type: typeUint256}, // chainId
}

// PackUserOpForHashV06 對 UserOperation 進行 v0.6 標準 10 欄位 ABI 打包 (320 位元組)
func PackUserOpForHashV06(op *UserOperation) ([]byte, error) {
	if op == nil {
		return nil, ErrNilUserOp
	}
	if err := op.Validate(); err != nil {
		return nil, err
	}

	hashInitCode := crypto.Keccak256Hash(op.InitCode)
	hashCallData := crypto.Keccak256Hash(op.CallData)
	hashPaymasterAndData := crypto.Keccak256Hash(op.PaymasterAndData)

	return userOpV06HashArgs.Pack(
		op.Sender,
		op.Nonce,
		hashInitCode,
		hashCallData,
		op.CallGasLimit,
		op.VerificationGasLimit,
		op.PreVerificationGas,
		op.MaxFeePerGas,
		op.MaxPriorityFeePerGas,
		hashPaymasterAndData,
	)
}

// PackUserOpForHashV07 對 PackedUserOperation 進行 v0.7 標準 8 欄位 ABI 打包 (256 位元組)
func PackUserOpForHashV07(op *PackedUserOperation) ([]byte, error) {
	if err := op.Validate(); err != nil {
		return nil, err
	}

	hashInitCode := crypto.Keccak256Hash(op.InitCode)
	hashCallData := crypto.Keccak256Hash(op.CallData)
	hashPaymasterAndData := crypto.Keccak256Hash(op.PaymasterAndData)

	return userOpV07HashArgs.Pack(
		op.Sender,
		op.Nonce,
		hashInitCode,
		hashCallData,
		op.AccountGasLimits,
		op.PreVerificationGas,
		op.GasFees,
		hashPaymasterAndData,
	)
}

// PackOuterUserOpHash 對 innerHash, entryPoint, chainId 進行外層 ABI 打包 (96 位元組)
func PackOuterUserOpHash(innerHash common.Hash, entryPoint common.Address, chainID *big.Int) ([]byte, error) {
	if entryPoint == (common.Address{}) {
		return nil, ErrInvalidEntryPoint
	}
	if chainID == nil || chainID.Sign() <= 0 {
		return nil, ErrInvalidChainID
	}
	if chainID.BitLen() > 256 {
		return nil, ErrUint256Overflow
	}
	return userOpOuterHashArgs.Pack(innerHash, entryPoint, chainID)
}
