package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
)

const (
	WALLET_DATA_PREFIX = "wallet_"
	WALLET_DATA_EXT    = ".data"

	SOL  = "So11111111111111111111111111111111111111112"
	USDC = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	USDT = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
)

// WalletStore represents a single wallet's stored data
type WalletStore struct {
	Name   string               `json:"name"`
	PubKey solana.PublicKey     `json:"pubkey"`
	Token  map[string]TokenInfo `json:"tokens"`
	Skip   map[string]TokenInfo `json:"skip"`
}

// calculate the amount of token in lamports
func UiToLamport(amount float64, mint string, wallet *Wallet) int64 {
	var decimal float64
	if mint == SOL {
		decimal = 9
	} else {
		outputTokendata, exist := wallet.Token[mint]
		if !exist {
			decimal = 6
			log.Printf("No metadata found for mint: %s. Using standard decimals: %f\n", mint, decimal)
		} else {
			decimal = float64(outputTokendata.Decimals)
		}
	}

	return int64(math.Round(amount * math.Pow(10, decimal)))
}

// calculate the amount of token in UI
func LamportToUi(amount int64, mint string, wallet *Wallet) float64 {
	var decimal float64
	if mint == SOL {
		decimal = 9
	} else {
		outputTokendata, exist := wallet.Token[mint]
		if !exist {
			decimal = 6
			log.Printf("No metadata found for mint: %s. Using standard decimals: %f\n", mint, decimal)
		} else {
			decimal = float64(outputTokendata.Decimals)
		}
	}

	return float64(amount) / math.Pow(10, decimal)
}

// SaveWallets saves each wallet's data to a separate file
func SaveWallets() error {
	for addr, wallet := range walletMap {
		store := WalletStore{
			Name:   wallet.Name,
			PubKey: wallet.PubKey,
			Token:  wallet.Token,
			Skip:   wallet.Skip,
		}

		filename := fmt.Sprintf("%s%s%s", WALLET_DATA_PREFIX, addr[:8], WALLET_DATA_EXT)
		data, err := json.MarshalIndent(store, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal wallet data for %s: %v", addr, err)
		}

		if err := os.WriteFile(filename, data, 0644); err != nil {
			return fmt.Errorf("failed to write wallet data to file %s: %v", filename, err)
		}

		if verbose {
			log.Printf("Saved wallet data for %s to %s\n", addr, filename)
		}
	}

	log.Printf("Saved data for %d wallets\n", len(walletMap))
	return nil
}

// LoadWallets loads wallet data from separate files
func LoadWallets() error {
	loadedCount := 0

	// For each wallet in config, try to load its data file
	for _, addr := range config.Monitor.Wallets {
		filename := fmt.Sprintf("%s%s%s", WALLET_DATA_PREFIX, addr[:8], WALLET_DATA_EXT)

		data, err := os.ReadFile(filename)
		if err != nil {
			// Initialize empty wallet if file doesn't exist
			walletMap[addr] = &Wallet{
				Name:   "", // Can be set later if needed
				PubKey: solana.MustPublicKeyFromBase58(addr),
				Token:  make(map[string]TokenInfo),
				Skip:   make(map[string]TokenInfo),
			}
			if verbose {
				log.Printf("Initialized empty data for wallet %s\n", addr)
			}
			continue
		}

		var store WalletStore
		if err := json.Unmarshal(data, &store); err != nil {
			log.Printf("Warning: failed to unmarshal wallet data from %s: %v\n", filename, err)
			continue
		}

		// Verify this is the correct wallet file using PubKey instead of Address
		if store.PubKey.String() != addr {
			continue
		}

		// Initialize or update wallet in walletMap
		walletMap[addr] = &Wallet{
			Name:   store.Name,
			PubKey: store.PubKey,
			Token:  store.Token,
			Skip:   store.Skip,
		}

		loadedCount++
		if verbose {
			log.Printf("Loaded wallet data for %s from %s\n", addr, filename)
		}
	}

	log.Printf("Loaded data for %d wallets\n", loadedCount)
	return nil
}

// retryRPC retries an RPC call with exponential backoff on rate limit errors
func retryRPC(fn func() error) error {
	const retries = 3
	delay := 5 * time.Second

	var err error
	for i := 0; i < retries; i++ {
		if err = fn(); err == nil {
			return nil // Success, exit
		}

		// Check if the error is transient and worth retrying
		if isTransientError(err) {
			if verbose {
				// Log the function name or HTTP request details
				pc, _, _, _ := runtime.Caller(2) // Get the caller's function name
				funcName := runtime.FuncForPC(pc).Name()
				log.Printf("RPC faied! [%s] Retrying in %v... (attempt %d/%d)", funcName, delay, i+1, retries)
			}
			time.Sleep(delay)
			delay *= 2
			if delay > 15*time.Second {
				delay = 15 * time.Second
			}
		} else {
			break
		}
	}
	// If the error is of type *jsonrpc.RPCError, print the message
	if rpcErr, ok := err.(*jsonrpc.RPCError); ok {
		return fmt.Errorf("RPC error: %s", rpcErr.Message)
	}
	return err
}

// isTransientError checks if an error is transient and worth retrying
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for rate limit errors (HTTP 429)
	if strings.Contains(err.Error(), "many requests") || strings.Contains(err.Error(), "429") {
		return true
	}

	// Check for network-related errors
	if strings.Contains(err.Error(), "connection reset by peer") ||
		strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "temporary failure") {
		return true
	}

	// Check for server errors (HTTP 5xx)
	if strings.Contains(err.Error(), "500") ||
		strings.Contains(err.Error(), "502") ||
		strings.Contains(err.Error(), "503") {
		return true
	}

	// Check for context deadline exceeded
	if err == context.DeadlineExceeded {
		return true
	}

	// Add other transient errors as needed
	return false
}

var Positions []position

type position struct {
	mint   string
	amount int64
	time   time.Time
}

func addPosition(mint string, amount int64) {
	newPosition := position{
		mint:   mint,
		amount: amount,
		time:   time.Now(),
	}
	Positions = append(Positions, newPosition)
}

func removePosition(mint string) {
	for i, pos := range Positions {
		if pos.mint == mint {
			Positions = append(Positions[:i], Positions[i+1:]...)
		}
	}
}

func getPositionAmount(mint string) int64 {
	for _, pos := range Positions {
		if pos.mint == mint {
			return pos.amount
		}
	}
	return 0
}
