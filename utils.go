package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

const FILE_TOKEN_DATA = "token_data.json"

type TokenStore struct {
	TokenData  map[string]TokenInfo `json:"tokenData"`
	SkipTokens map[string]bool      `json:"skipTokens"`
}

type Tokens map[string]TokenInfo

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
	const retries = 9
	delay := 5 * time.Second

	var err error
	for i := 0; i < retries; i++ {
		if err = fn(); err == nil {
			return nil // Success, exit
		}

		// Check if the error is a rate limit error (HTTP 429)
		if strings.Contains(err.Error(), "many requests") || strings.Contains(err.Error(), "429") {
			if debug {
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

		// If not a rate limit error, return it
		return err
	}

	return err
}

// FormatNumber formats a float64 with custom thousand separator, decimal places, and optional sign
func FormatNumber(value float64, decimalPlaces int, thousandSeparator string, showSign bool) string {
	// Determine the sign
	sign := ""
	if showSign && value > 0 {
		sign = "+"
	} else if value < 0 {
		sign = "-"
		value = -value // Make the value positive for formatting
	}

	// Format the number with fixed decimal places
	str := fmt.Sprintf("%.*f", decimalPlaces, value)

	// Split into integer and fractional parts
	parts := strings.Split(str, ".")
	integerPart := parts[0]
	fractionalPart := ""
	if len(parts) > 1 {
		fractionalPart = "." + parts[1]
	}

	// Add thousand separators to the integer part
	n := len(integerPart)
	for i := n - 3; i > 0; i -= 3 {
		integerPart = integerPart[:i] + thousandSeparator + integerPart[i:]
	}

	// Combine sign, integer part, and fractional part
	return sign + integerPart + fractionalPart
}
