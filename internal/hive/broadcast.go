package hive

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"log"
	"math/big"
	"strings"
	"time"
)

// TransactionBuilder builds, signs and broadcasts Hive transactions using
// the server-side serializer (condenser_api.get_transaction_hex).
type TransactionBuilder struct {
	client *HiveClient
}

// NewTransactionBuilder creates a builder bound to the given client.
func NewTransactionBuilder(client *HiveClient) *TransactionBuilder {
	return &TransactionBuilder{client: client}
}

// Broadcast signs operations with the given WIF key and broadcasts the
// transaction. It returns the transaction id. With dryRun the transaction is
// built but not signed or broadcast.
func (t *TransactionBuilder) Broadcast(ctx context.Context, operations [][]any, wif string, dryRun bool) (string, error) {
	props, err := t.client.GetDynamicGlobalProperties(ctx)
	if err != nil {
		return "", err
	}
	lib, _ := toInt64(props["last_irreversible_block_num"])

	refBlockNum := (lib - 1) & 0xFFFF

	header, err := t.client.GetBlockHeader(ctx, lib)
	if err != nil {
		return "", err
	}
	headBlockID := "0000000000000000000000000000000000000000"
	if header != nil {
		if prev, _ := header["previous"].(string); prev != "" {
			headBlockID = prev
		}
	}
	prevBytes, err := hex.DecodeString(headBlockID)
	if err != nil || len(prevBytes) < 8 {
		return "", NewError("malformed block id %q", headBlockID)
	}
	refBlockPrefix := binary.LittleEndian.Uint32(prevBytes[4:8])

	nodeTime, _ := props["time"].(string)
	expiry, err := parseHiveTime(nodeTime)
	if err != nil {
		return "", NewError("malformed node time %q: %v", nodeTime, err)
	}
	expiration := expiry.Add(600 * time.Second).UTC().Format("2006-01-02T15:04:05")

	tx := map[string]any{
		"ref_block_num":    refBlockNum,
		"ref_block_prefix": refBlockPrefix,
		"expiration":       expiration,
		"operations":       operations,
		"extensions":       []any{},
	}

	txHex, err := t.client.RPCCall(ctx, "condenser_api.get_transaction_hex", []any{tx})
	if err != nil {
		return "", err
	}
	var hexStr string
	if err := json.Unmarshal(txHex, &hexStr); err != nil {
		return "", NewError("malformed get_transaction_hex response: %v", err)
	}
	if len(hexStr) < 2 {
		return "", NewError("get_transaction_hex returned nothing to sign")
	}

	// digest = sha256(chain_id + serialized_tx_without_trailing_signature_byte)
	raw, err := hex.DecodeString(ChainIDHIVE + hexStr[:len(hexStr)-2])
	if err != nil {
		return "", NewError("malformed transaction hex: %v", err)
	}
	digest := sha256.Sum256(raw)

	secret, err := wifToSecret(wif)
	if err != nil {
		return "", NewValidationError("%v", err)
	}
	sigHex, err := signDigestHive(digest[:], secret)
	if err != nil {
		return "", NewError("signing failed: %v", err)
	}

	tx["signatures"] = []string{sigHex}

	if dryRun {
		log.Printf("DRY RUN - transaction not broadcast")
		return "", nil
	}

	if _, err := t.client.RPCCall(ctx, "condenser_api.broadcast_transaction", []any{tx}); err != nil {
		return "", err
	}

	// tx id = sha256(serialized tx)[:20]
	rawTx, err := hex.DecodeString(hexStr[:len(hexStr)-2])
	if err != nil {
		return "", NewError("malformed transaction hex: %v", err)
	}
	txSum := sha256.Sum256(rawTx)
	return hex.EncodeToString(txSum[:20]), nil
}

// GetAccountRC returns the account's Resource Credits as a percentage (0-100).
func GetAccountRC(ctx context.Context, client *HiveClient, account string) (float64, error) {
	result, err := client.RPCCall(ctx, "rc_api.find_rc_accounts", []any{map[string]any{"accounts": []string{account}}})
	if err != nil {
		return 0, err
	}
	var resp struct {
		RCAccounts []struct {
			RCManabar struct {
				CurrentMana    string `json:"current_mana"`
				LastUpdateTime int64  `json:"last_update_time"`
			} `json:"rc_manabar"`
			MaxRC string `json:"max_rc"`
		} `json:"rc_accounts"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return 0, NewError("malformed find_rc_accounts response: %v", err)
	}
	if len(resp.RCAccounts) == 0 {
		return 0, NewError("account %s not found", account)
	}
	rc := resp.RCAccounts[0]

	lastMana, ok1 := stringsToBig(rc.RCManabar.CurrentMana)
	maxMana, ok2 := stringsToBig(rc.MaxRC)
	if !ok1 || !ok2 || maxMana.Sign() == 0 {
		return 0, NewError("malformed RC data for account %s", account)
	}

	// Regenerated mana: 5 days (VOTING_MANA_REGENERATION_IN_SECONDS).
	const regenerationSeconds = 5 * 60 * 60 * 24
	updatedAt := time.Unix(rc.RCManabar.LastUpdateTime, 0)
	diffSeconds := time.Since(updatedAt).Seconds()
	if diffSeconds < 0 {
		diffSeconds = 0
	}

	lastFloat, _ := lastMana.Float64()
	maxFloat, _ := maxMana.Float64()
	regenerated := diffSeconds * maxFloat / regenerationSeconds
	current := lastFloat + regenerated

	percent := current * 100 / maxFloat
	// Cap at 100 (mana cannot exceed max).
	if percent > 100 {
		percent = 100
	}
	return percent, nil
}

func stringsToBig(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	v, ok := new(big.Int).SetString(s, 10)
	return v, ok
}
