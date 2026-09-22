package wallet

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// finalizedNonce is request-local evidence, never persisted as a payment state
// or reused after restart. An unavailable RPC therefore restores the protection.
type finalizedNonce struct {
	account common.Address
	chainID int64
	nonce   uint64
}

func (f *finalizedNonce) consumes(record *JournalRecord) bool {
	if f == nil || record.Nonce >= f.nonce {
		return false
	}
	raw, err := hexutil.Decode(record.SignedRaw)
	var tx types.Transaction
	if err != nil || tx.UnmarshalBinary(raw) != nil || tx.Hash().Hex() != string(record.Hash) ||
		tx.ChainId().Cmp(big.NewInt(f.chainID)) != 0 || tx.Nonce() != record.Nonce {
		return false
	}
	sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), &tx)
	return err == nil && sender == f.account
}

func (s *Service) finalizedNonce(ctx context.Context) *finalizedNonce {
	address, err := s.keystore.Address()
	if err != nil || !common.IsHexAddress(address) {
		return nil
	}
	account := common.HexToAddress(address)
	nonce, err := s.client.FinalizedNonceAt(ctx, account)
	if err != nil || ctx.Err() != nil {
		return nil
	}
	return &finalizedNonce{account: account, chainID: s.client.ChainID(), nonce: nonce}
}

// Called under sendMu for both quote and send. A newer local transaction must
// still block, even if an older nonce was consumed. No automatic retry is made.
func (s *Service) checkInFlight(ctx context.Context) (*finalizedNonce, error) {
	if !s.journal.HasInFlightTx() {
		return nil, nil
	}
	finalized := s.finalizedNonce(ctx)
	if s.journal.hasInFlightTx(finalized) {
		return nil, ErrTxInFlight
	}
	return finalized, nil
}
