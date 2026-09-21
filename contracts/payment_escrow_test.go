package contracts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
)

type escrowFixture struct {
	t                     *testing.T
	chain                 *simulated.Backend
	people                []*bind.TransactOpts
	token, escrow         *bind.BoundContract
	tokenAddress, address common.Address
	escrowABI             abi.ABI
}

func newEscrowFixture(t *testing.T) *escrowFixture {
	t.Helper()
	f := &escrowFixture{t: t}
	alloc := types.GenesisAlloc{}
	for range 3 {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		signer, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337))
		if err != nil {
			t.Fatal(err)
		}
		f.people = append(f.people, signer)
		alloc[signer.From] = types.Account{Balance: new(big.Int).Exp(big.NewInt(10), big.NewInt(21), nil)}
	}
	f.chain = simulated.NewBackend(alloc)
	t.Cleanup(func() { f.chain.Close() })
	f.tokenAddress, f.token, _ = f.deploy("EscrowTestToken")
	f.address, f.escrow, f.escrowABI = f.deploy("PaymentEscrow", f.tokenAddress)
	for _, person := range f.people {
		f.send(f.token, f.people[0], true, "mint", person.From, big.NewInt(1_000_000))
		f.send(f.token, person, true, "approve", f.address, big.NewInt(1_000_000))
	}
	return f
}

func (f *escrowFixture) deploy(name string, args ...any) (common.Address, *bind.BoundContract, abi.ABI) {
	f.t.Helper()
	raw, err := os.ReadFile("artifacts/" + name + ".json")
	if err != nil {
		f.t.Fatal(err)
	}
	var artifact struct {
		ABI                              json.RawMessage
		Bytecode, SourceFile, SourceHash string
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		f.t.Fatal(err)
	}
	source, err := os.ReadFile(artifact.SourceFile)
	if err != nil {
		f.t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(source)) != artifact.SourceHash {
		f.t.Fatal("stale artifact")
	}
	parsed, err := abi.JSON(strings.NewReader(string(artifact.ABI)))
	if err != nil {
		f.t.Fatal(err)
	}
	addr, tx, contract, err := bind.DeployContract(f.people[0], parsed, common.FromHex(artifact.Bytecode), f.chain.Client(), args...)
	if err != nil {
		f.t.Fatal(err)
	}
	f.chain.Commit()
	receipt, err := f.chain.Client().TransactionReceipt(context.Background(), tx.Hash())
	if err != nil || receipt.Status != 1 {
		f.t.Fatalf("deploy: %v", err)
	}
	return addr, contract, parsed
}

func (f *escrowFixture) send(contract *bind.BoundContract, person *bind.TransactOpts, success bool, method string, args ...any) *types.Receipt {
	f.t.Helper()
	opts := *person
	opts.GasLimit = 1_000_000
	tx, err := contract.Transact(&opts, method, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	f.chain.Commit()
	receipt, err := f.chain.Client().TransactionReceipt(context.Background(), tx.Hash())
	if err != nil {
		f.t.Fatal(err)
	}
	if (receipt.Status == 1) != success {
		f.t.Fatalf("%s success=%v want=%v", method, receipt.Status, success)
	}
	return receipt
}

func (f *escrowFixture) call(contract *bind.BoundContract, method string, args ...any) []any {
	f.t.Helper()
	var out []any
	if err := contract.Call(&bind.CallOpts{}, &out, method, args...); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *escrowFixture) state(buyer common.Address, id common.Hash, want uint8) {
	f.t.Helper()
	if got := f.call(f.escrow, "orders", buyer, id)[2].(uint8); got != want {
		f.t.Fatalf("state=%d want=%d", got, want)
	}
}

func TestPaymentEscrowLifecycleAndPermissions(t *testing.T) {
	f := newEscrowFixture(t)
	buyer, seller, other := f.people[0], f.people[1], f.people[2]
	id := crypto.Keccak256Hash([]byte("order-001"))
	amount := big.NewInt(120_000)
	receipt := f.send(f.escrow, buyer, true, "fund", id, seller.From, amount)
	event := receipt.Logs[len(receipt.Logs)-1]
	if event.Address != f.address || len(event.Topics) != 4 || event.Topics[0] != f.escrowABI.Events["Funded"].ID || event.Topics[1] != common.BytesToHash(buyer.From.Bytes()) || event.Topics[2] != id || event.Topics[3] != common.BytesToHash(seller.From.Bytes()) || new(big.Int).SetBytes(event.Data).Cmp(amount) != 0 {
		t.Fatal("fund event mismatch")
	}
	f.send(f.escrow, buyer, false, "fund", id, seller.From, amount)
	f.send(f.escrow, other, false, "release", buyer.From, id)
	f.send(f.escrow, buyer, false, "refund", buyer.From, id)
	f.send(f.escrow, other, false, "refund", buyer.From, id)
	f.state(buyer.From, id, 1)
	// Another buyer can use the same reference without changing this buyer's order.
	f.send(f.escrow, other, true, "fund", id, seller.From, big.NewInt(33))
	f.send(f.escrow, buyer, true, "release", buyer.From, id)
	f.state(buyer.From, id, 2)
	f.state(other.From, id, 1)
	f.send(f.escrow, seller, false, "refund", buyer.From, id)
	f.send(f.escrow, buyer, false, "release", buyer.From, id)
	f.send(f.escrow, buyer, false, "fund", id, seller.From, amount)
	f.send(f.escrow, seller, true, "refund", other.From, id)
	f.state(other.From, id, 3)
	f.send(f.escrow, seller, false, "refund", other.From, id)
	if f.call(f.token, "balanceOf", seller.From)[0].(*big.Int).Int64() != 1_120_000 || f.call(f.token, "balanceOf", other.From)[0].(*big.Int).Int64() != 1_000_000 || f.call(f.escrow, "totalLocked")[0].(*big.Int).Sign() != 0 {
		t.Fatal("settlement balances")
	}
	for _, args := range [][]any{{common.Hash{}, seller.From, amount}, {crypto.Keccak256Hash([]byte("zero")), seller.From, big.NewInt(0)}, {id, common.Address{}, amount}, {id, buyer.From, amount}, {id, f.address, amount}} {
		f.send(f.escrow, buyer, false, "fund", args...)
	}
}

func TestPaymentEscrowTokenFailuresAndReentry(t *testing.T) {
	for _, mode := range []uint8{1, 2, 3, 4} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			f := newEscrowFixture(t)
			buyer, seller := f.people[0], f.people[1]
			id := crypto.Keccak256Hash([]byte("token-failure"))
			amount := big.NewInt(100)
			data, err := f.escrowABI.Pack("refund", buyer.From, id)
			if err != nil {
				t.Fatal(err)
			}
			f.send(f.token, buyer, true, "configure", mode, f.address, data)
			f.send(f.escrow, buyer, mode == 4, "fund", id, seller.From, amount)
			if mode != 4 {
				f.state(buyer.From, id, 0)
				if f.call(f.escrow, "totalLocked")[0].(*big.Int).Sign() != 0 || f.call(f.token, "balanceOf", buyer.From)[0].(*big.Int).Int64() != 1_000_000 {
					t.Fatal("failed funding changed balances")
				}
				f.send(f.token, buyer, true, "configure", uint8(0), f.address, []byte{})
				f.send(f.escrow, buyer, true, "fund", id, seller.From, amount)
				f.send(f.token, buyer, true, "configure", mode, f.address, data)
				f.send(f.escrow, buyer, false, "release", buyer.From, id)
				f.send(f.escrow, seller, false, "refund", buyer.From, id)
				f.state(buyer.From, id, 1)
				if f.call(f.escrow, "totalLocked")[0].(*big.Int).Int64() != 100 || f.call(f.token, "balanceOf", f.address)[0].(*big.Int).Int64() != 100 {
					t.Fatal("failed settlement changed accounting")
				}
			} else {
				f.send(f.escrow, seller, true, "refund", buyer.From, id)
				if f.call(f.token, "callbackSucceeded")[0].(bool) {
					t.Fatal("reentry succeeded")
				}
				result := f.call(f.token, "callbackResult")[0].([]byte)
				if !bytes.Equal(result, crypto.Keccak256([]byte("ReentrancyGuardReentrantCall()"))[:4]) {
					t.Fatal("reentry was not rejected by guard")
				}
				f.state(buyer.From, id, 3)
			}
		})
	}
}

func TestPaymentEscrowRandomizedAccounting(t *testing.T) {
	f := newEscrowFixture(t)
	rng := rand.New(rand.NewPCG(42, 7))
	type item struct {
		buyer, seller int
		id            common.Hash
		amount        int64
		state         uint8
	}
	orders := make([]item, 12)
	for i := range orders {
		orders[i] = item{buyer: i % 3, seller: (i + 1) % 3, id: crypto.Keccak256Hash(fmt.Append(nil, i)), amount: int64(i+1) * 17}
	}
	expected := []int64{1_000_000, 1_000_000, 1_000_000}
	for range 100 {
		i := rng.IntN(len(orders))
		o := &orders[i]
		actor := rng.IntN(3)
		action := rng.IntN(3)
		switch action {
		case 0:
			success := o.state == 0
			f.send(f.escrow, f.people[o.buyer], success, "fund", o.id, f.people[o.seller].From, big.NewInt(o.amount))
			if success {
				o.state = 1
				expected[o.buyer] -= o.amount
			}
		case 1:
			success := o.state == 1 && actor == o.buyer
			f.send(f.escrow, f.people[actor], success, "release", f.people[o.buyer].From, o.id)
			if success {
				o.state = 2
				expected[o.seller] += o.amount
			}
		case 2:
			success := o.state == 1 && actor == o.seller
			f.send(f.escrow, f.people[actor], success, "refund", f.people[o.buyer].From, o.id)
			if success {
				o.state = 3
				expected[o.buyer] += o.amount
			}
		}
		var locked int64
		for _, order := range orders {
			f.state(f.people[order.buyer].From, order.id, order.state)
			if order.state == 1 {
				locked += order.amount
			}
		}
		if f.call(f.escrow, "totalLocked")[0].(*big.Int).Int64() != locked || f.call(f.token, "balanceOf", f.address)[0].(*big.Int).Int64() != locked {
			t.Fatal("escrow liabilities diverged")
		}
		for j, p := range f.people {
			if f.call(f.token, "balanceOf", p.From)[0].(*big.Int).Int64() != expected[j] {
				t.Fatal("account balance diverged")
			}
		}
	}
}
