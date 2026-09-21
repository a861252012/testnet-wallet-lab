package web

import (
	"encoding/json"
	"net/http"
)

type keyService interface {
	Backup(password string) (json.RawMessage, error)
	RestoreBackup(raw json.RawMessage, password, newPassword string) error
	ChangePassword(password, newPassword string) error
}

func registerKeyRoutes(mux *http.ServeMux, csrf string, service keyService) {
	mux.HandleFunc("POST /api/backup", localWalletFilter(csrf, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password string `json:"password"`
		}
		if err := decodeStrictJSON(w, r, &req); err != nil {
			respondWallet(w, 400, nil, err)
			return
		}
		if !allowSharedPasswordAttempt(w, r, req.Password) {
			return
		}
		backup, err := service.Backup(req.Password)
		if err != nil {
			respondWallet(w, 400, nil, err)
			return
		}
		respondWallet(w, 200, backup, nil)
	}))
	mux.HandleFunc("POST /api/restore", localWalletFilter(csrf, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Backup      json.RawMessage `json:"backup"`
			Password    string          `json:"password"`
			NewPassword string          `json:"newPassword"`
		}
		if err := decodeStrictJSON(w, r, &req); err != nil {
			respondWallet(w, 400, nil, err)
			return
		}
		if err := service.RestoreBackup(req.Backup, req.Password, req.NewPassword); err != nil {
			respondWallet(w, 400, nil, err)
			return
		}
		respondWallet(w, 200, &walletMutationResponse{Restored: true}, nil)
	}))
	mux.HandleFunc("POST /api/password", localWalletFilter(csrf, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password    string `json:"password"`
			NewPassword string `json:"newPassword"`
		}
		if err := decodeStrictJSON(w, r, &req); err != nil {
			respondWallet(w, 400, nil, err)
			return
		}
		if err := service.ChangePassword(req.Password, req.NewPassword); err != nil {
			respondWallet(w, 400, nil, err)
			return
		}
		respondWallet(w, 200, &walletMutationResponse{Changed: true}, nil)
	}))
}
