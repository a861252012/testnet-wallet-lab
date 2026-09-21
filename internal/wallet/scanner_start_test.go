package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestScannerStartSelectionSurvivesRestart(t *testing.T) {
	zero := uint64(0)
	for _, tc := range []struct {
		name        string
		start       *uint64
		restart     bool
		failFirst   bool
		legacy      string
		wantStart   uint64
		wantScanned uint64
	}{
		{name: "explicit zero", start: &zero},
		{name: "explicit zero after restart", start: &zero, restart: true},
		{name: "explicit zero retry after restart", start: &zero, restart: true, failFirst: true},
		{name: "unspecified starts recently", wantStart: 81, wantScanned: 81},
		{name: "unspecified after restart", restart: true, wantStart: 81, wantScanned: 81},
		{name: "legacy unspecified", legacy: `{"enabled":true,"start":0,"next":0,"tokens":[]}`, wantStart: 81, wantScanned: 81},
		{name: "legacy existing cursor", legacy: `{"enabled":true,"start":20,"next":42,"tokens":[]}`, wantStart: 20, wantScanned: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scanned := make(chan uint64, 4)
			assertScanned := func(want uint64) {
				t.Helper()
				select {
				case got := <-scanned:
					if got != want {
						t.Fatalf("scanned block %d, want %d", got, want)
					}
				default:
					t.Fatalf("block %d was not scanned", want)
				}
			}
			var attempts atomic.Int32
			header := func(number uint64) *types.Header {
				return &types.Header{Number: new(big.Int).SetUint64(number), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}
			}
			client := mockRPC(t, func(method string, params json.RawMessage) any {
				switch method {
				case "eth_chainId":
					return "0xaa36a7"
				case "eth_getBlockByNumber":
					var args []json.RawMessage
					if err := json.Unmarshal(params, &args); err != nil || len(args) != 2 {
						return errors.New("invalid block parameters")
					}
					var tag string
					if err := json.Unmarshal(args[0], &tag); err != nil {
						return err
					}
					if tag == "finalized" {
						return header(100)
					}
					number, err := hexutil.DecodeUint64(tag)
					if err != nil {
						return err
					}
					var full bool
					if err := json.Unmarshal(args[1], &full); err != nil {
						return err
					}
					if full {
						scanned <- number
						if attempts.Add(1) == 1 && tc.failFirst {
							return errors.New("temporary block failure")
						}
						return map[string]any{"hash": header(number).Hash(), "transactions": []any{}}
					}
					return header(number)
				case "eth_getBlockReceipts":
					return []any{}
				}
				return nil
			})
			dir := t.TempDir()
			svc, err := NewService(client, dir, 2, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = svc.Close() }()
			if _, err := svc.Create("test-password-123"); err != nil {
				t.Fatal(err)
			}
			if tc.legacy != "" {
				if err := os.WriteFile(filepath.Join(dir, "scan.json"), []byte(tc.legacy), 0600); err != nil {
					t.Fatal(err)
				}
			} else if _, err := svc.ConfigureScan(true, tc.start); err != nil {
				t.Fatal(err)
			}
			if tc.failFirst {
				if err := svc.ScanOnce(context.Background()); err == nil {
					t.Fatal("failed block query was ignored")
				}
				assertScanned(0)
				progress, err := svc.ScanProgress()
				if err != nil || progress.Next != 0 || progress.Error == "" {
					t.Fatalf("failure changed cursor or lost error: %+v %v", progress, err)
				}
			}
			if tc.restart {
				if err := svc.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := NewService(client, dir, 2, 1)
				if err != nil {
					t.Fatal(err)
				}
				svc = reopened
				// Toggling scan without a new start must retain the configured cursor.
				if _, err := svc.ConfigureScan(false, nil); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.ConfigureScan(true, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := svc.ScanOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertScanned(tc.wantScanned)
			progress, err := svc.ScanProgress()
			if err != nil || progress.Start != tc.wantStart || progress.Next != tc.wantScanned+1 || progress.Error != "" {
				t.Fatalf("unexpected progress: %+v %v", progress, err)
			}
			response, err := json.Marshal(progress)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(response, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["explicitStart"]; exists {
				t.Fatal("disk-only start flag leaked into API response")
			}
		})
	}
}
