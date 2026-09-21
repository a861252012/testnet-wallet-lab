package e2e

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

func TestE2EEscrowRejectsStaleGasBeforeBroadcast(t *testing.T) {
	f := newEscrowE2E(t)
	f.approve(0, "5000000")
	f.send(0, f.quote(0, map[string]string{
		"action": "escrow_fund", "to": f.addresses[1].Hex(),
		"orderId": "gas-change", "amount": "5",
	}, 200))
	f.history(0)
	releaseRequest := map[string]string{
		"action": "escrow_release", "to": f.addresses[1].Hex(),
		"buyer": f.addresses[0].Hex(), "orderId": "gas-change",
	}
	quote := f.quote(0, releaseRequest, 200)
	// The recipient spends its balance while the payer is reviewing the quote.
	f.send(1, f.quote(1, map[string]string{
		"action": "transfer", "to": f.addresses[0].Hex(),
		"contract": f.tokenAddress.Hex(), "amountRaw": "100000000",
	}, 200))
	f.history(1)
	if balance := f.call(f.token, "balanceOf", f.addresses[1])[0].(*big.Int); balance.Sign() != 0 {
		t.Fatal("fixture recipient balance must be zero", balance)
	}
	needed, err := f.client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: f.addresses[0], To: &f.contract, Value: big.NewInt(0),
		Data: hexutil.MustDecode(quote["data"].(string)),
	})
	if err != nil {
		t.Fatal(err)
	}
	limit, err := strconv.ParseUint(quote["gasLimit"].(string), 10, 64)
	if err != nil || needed <= limit {
		t.Fatalf("fixture must increase gas beyond quote: needed=%d limit=%d err=%v", needed, limit, err)
	}
	before := f.sendCount.Load()
	rejected := f.request(0, "POST", "/api/wallet/send", map[string]any{
		"quoteId": quote["id"], "password": escrowPassword,
	}, 400)
	if rejected["code"] != "send_rejected" || !strings.Contains(rejected["error"].(string), "Gas") {
		t.Fatal("expected a safe rejection requiring a fresh gas quote", rejected)
	}
	if f.sendCount.Load() != before || f.order(0, "gas-change")["state"] != "funded" {
		t.Fatal("stale gas quote must not broadcast or change the order")
	}
	// Rejection must not leave an in-flight record that blocks a fresh quote.
	fresh := f.quote(0, releaseRequest, 200)
	sent := f.send(0, fresh)
	f.checkReceipt(sent, "Released", "gas-change", 5_000_000)
	if f.order(0, "gas-change")["state"] != "released" {
		t.Fatal("freshly quoted release did not complete")
	}
}
