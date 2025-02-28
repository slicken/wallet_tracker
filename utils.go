package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	FILE_TOKEN_DATA = "token_data.json"
	USDC            = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	SOL             = "So11111111111111111111111111111111111111112"
)

type TokenStore struct {
	TokenData  map[string]TokenInfo `json:"tokenData"`
	SkipTokens map[string]bool      `json:"skipTokens"`
}

var (
	// token data
	tokenData  = make(map[string]TokenInfo)
	skipTokens = make(map[string]bool)
)

// Copy Token map
func CopyTokens(tokens map[string]TokenInfo) map[string]TokenInfo {
	previousTokens := make(map[string]TokenInfo)
	for k, v := range tokens {
		previousTokens[k] = v
	}
	return previousTokens
}

// Saves token data to a file
func SaveTokenData() error {
	// Create the merged data structure
	store := TokenStore{
		TokenData:  tokenData,
		SkipTokens: skipTokens,
	}

	// Convert the struct to JSON with indentation
	data, err := json.MarshalIndent(store, "", "  ") // Indent with 2 spaces
	if err != nil {
		return fmt.Errorf("failed to marshal token store: %v", err)
	}

	// Write the JSON data to the file
	err = os.WriteFile(FILE_TOKEN_DATA, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write token store to file: %v", err)
	}

	log.Println("Token store saved successfully!")
	return nil
}

// Loads token data from file
func LoadTokenData() error {
	// Initialize tokenData and skipTokens
	tokenData = make(map[string]TokenInfo)
	skipTokens = make(map[string]bool)

	// Check if the file exists
	if _, err := os.Stat(FILE_TOKEN_DATA); os.IsNotExist(err) {
		log.Printf("Warning: %s not found. Initialized empty token store.\n", FILE_TOKEN_DATA)
		return nil
	}

	// Read the file content
	data, err := os.ReadFile(FILE_TOKEN_DATA)
	if err != nil {
		return fmt.Errorf("failed to read token store file: %v", err)
	}

	// Decode the JSON data into the TokenStore struct
	var store TokenStore
	err = json.Unmarshal(data, &store)
	if err != nil {
		return fmt.Errorf("failed to unmarshal token store: %v", err)
	}

	// Populate tokenData and skipTokens
	if store.TokenData != nil {
		tokenData = store.TokenData
	}
	if store.SkipTokens != nil {
		skipTokens = store.SkipTokens
	}

	log.Printf("Loaded token store from '%s'.\n", FILE_TOKEN_DATA)
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
				pc, _, _, _ := runtime.Caller(1) // Get the caller's function name
				funcName := runtime.FuncForPC(pc).Name()
				log.Printf("Rate limit hit [%s]. Retrying in %v... (attempt %d/%d)", funcName, delay, i+1, retries)
			}
			time.Sleep(delay)
			delay *= 2
			if delay > 15*time.Second {
				delay = 15 * time.Second
			}
			continue
		}

		//return it if error is not rate limit
		return err
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
	amount float64
	time   time.Time
}

func addPosition(mint string, amount float64) {
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

func getPositionAmount(mint string) float64 {
	for _, pos := range Positions {
		if pos.mint == mint {
			return pos.amount
		}
	}
	return 0
}
