//go:build ignore

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const evidence = "docs/evidence/vault-sepolia-2026-09-19/"
const address = "0x11A0130EecDF4648efed6a0E3A8901Bf9E44B293"

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	c, e := ethclient.DialContext(ctx, "https://ethereum-sepolia-rpc.publicnode.com")
	must(e)
	id, e := c.ChainID(ctx)
	must(e)
	if id.Cmp(big.NewInt(11155111)) != 0 {
		panic("Wrong chain")
	}
	b, e := os.ReadFile(evidence + "compiled-ETHVault.json")
	must(e)
	var a struct {
		Evm struct {
			Bytecode         struct{ Object string }
			DeployedBytecode struct{ Object string }
		}
	}
	must(json.Unmarshal(b, &a))
	code, e := hex.DecodeString(a.Evm.Bytecode.Object)
	must(e)
	addr := common.HexToAddress(address)
	n, e := c.PendingNonceAt(ctx, addr)
	must(e)
	confirmed, e := c.NonceAt(ctx, addr, nil)
	must(e)
	if n != 0 || confirmed != 0 {
		panic("Dedicated account nonce not zero; inspect previous transactions")
	}
	ca := crypto.CreateAddress(addr, n)
	runtime, e := c.CodeAt(ctx, ca, nil)
	must(e)
	if len(runtime) > 0 {
		panic("Expected deployment address already has code")
	}
	bal, e := c.BalanceAt(ctx, addr, nil)
	must(e)
	gas, e := c.EstimateGas(ctx, ethereum.CallMsg{From: addr, Data: code})
	must(e)
	gas = gas * 120 / 100
	if gas > 400000 {
		panic("Gas limit exceeds 400000")
	}
	h, e := c.HeaderByNumber(ctx, nil)
	must(e)
	tip, e := c.SuggestGasTipCap(ctx)
	must(e)
	fee := new(big.Int).Add(new(big.Int).Mul(h.BaseFee, big.NewInt(2)), tip)
	if fee.Cmp(big.NewInt(2000000000)) > 0 {
		fee.SetInt64(2000000000)
	}
	if new(big.Int).Add(h.BaseFee, tip).Cmp(fee) > 0 {
		panic("Current base fee plus tip exceeds fixed 2 gwei limit")
	}
	cost := new(big.Int).Mul(new(big.Int).SetUint64(gas), fee)
	if cost.Cmp(big.NewInt(700000000000000)) > 0 {
		panic("Max deploy cost exceeds 0.0007 ETH")
	}
	if bal.Cmp(new(big.Int).Add(cost, big.NewInt(200000000000000))) < 0 {
		panic("Insufficient gas reserve")
	}
	plan := map[string]any{"chainId": id.String(), "from": addr.Hex(), "contractAddress": ca.Hex(), "nonce": n, "balanceWei": bal.String(), "estimatedGasWith20PercentMargin": gas, "maxFeePerGasWei": fee.String(), "maxPriorityFeePerGasWei": tip.String(), "maxCostWei": cost.String(), "block": h.Number.String(), "valueWei": "0"}
	save("deployment-preflight.json", plan)
	if len(os.Args) != 2 || os.Args[1] != "broadcast" {
		fmt.Println("Preflight passed; no transaction signed or broadcast")
		return
	}
	if _, e := os.Stat(evidence + "deployment-submitted.json"); !os.IsNotExist(e) {
		panic("Deployment submission record exists; do not re-sign")
	}
	dir := filepath.Join(os.Getenv("HOME"), ".ssh/wallet-demo-secrets")
	b, e = os.ReadFile(filepath.Join(dir, "vault-acceptance-20260919.keystore.json"))
	must(e)
	pw, e := os.ReadFile(filepath.Join(dir, "vault-acceptance-20260919.password"))
	must(e)
	key, e := keystore.DecryptKey(b, strings.TrimSpace(string(pw)))
	if e != nil {
		panic("Cannot decrypt dedicated test key")
	}
	if key.Address != addr {
		panic("Key address mismatch")
	}
	tx, e := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: id, Nonce: n, GasTipCap: tip, GasFeeCap: fee, Gas: gas, Value: big.NewInt(0), Data: code}), types.LatestSignerForChainID(id), key.PrivateKey)
	must(e)
	raw, e := tx.MarshalBinary()
	must(e)
	f, e := os.OpenFile(filepath.Join(dir, "vault-deployment-20260919.signedtx"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	_, e = f.Write(raw)
	must(e)
	must(f.Close())
	plan["transactionHash"] = tx.Hash().Hex()
	plan["signedAt"] = time.Now().UTC().Format(time.RFC3339)
	save("deployment-submitted.json", plan)
	e = c.SendTransaction(ctx, tx)
	if e != nil {
		panic("Broadcast returned error; inspect recorded hash before retry")
	}
	fmt.Println("Broadcast ETHVault", tx.Hash().Hex(), "at", ca.Hex())
}
func save(name string, v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(evidence+name, append(b, '\n'), 0644))
	fmt.Println(string(b))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
