package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const FILE_TOKEN_DATA = "token_data.json"

type TokenInfo struct {
	Address  string  `json:"address,omitempty"`   // Token mint address
	Symbol   string  `json:"symbol,omitempty"`    // Token symbol
	Name     string  `json:"name,omitempty"`      // Token name
	Decimals int     `json:"decimals,omitempty"`  // Token decimals
	Balance  float64 `json:"balance,omitempty"`   // Token balance
	USDValue float64 `json:"usd_value,omitempty"` // Token balance in USD
	Price    float64 `json:"price,omitempty"`     // Token price
}

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

// Fetches token metadata, info and price from Jupiter.
func fetchTokenMetadata(mint string, fetchPrice bool) (TokenInfo, error) {
	// Check if the token info is already cached
	if cachedTokenInfo, exists := tokenData[mint]; exists && cachedTokenInfo.Name != "" {
		if !fetchPrice {
			return cachedTokenInfo, nil
		}

		// Fetch the price for the cached token
		usdPrice, err := fetchTokenPriceJupiter(mint)
		if err != nil {
			log.Printf("Failed to fetch price for token %s: %v", mint, err)
			cachedTokenInfo.Price = 0
			return cachedTokenInfo, err
		}

		// Update the cached token info with the new price
		cachedTokenInfo.Price = usdPrice
		return cachedTokenInfo, nil
	}

	// If not cached, fetch token info from Jupiter
	tokenInfo, err := fetchTokenInfoJupiter(mint)
	if err != nil || tokenInfo.Name == "" {
		return TokenInfo{}, err
	}

	if !fetchPrice {
		tokenData[mint] = tokenInfo
		return tokenInfo, nil
	}

	// Fetch token price using Jupiter API
	usdPrice, err := fetchTokenPriceJupiter(mint)
	if err != nil {
		tokenData[mint] = tokenInfo
		return tokenInfo, err
	}

	// Cache price
	tokenInfo.Price = usdPrice
	tokenData[mint] = tokenInfo
	return tokenInfo, err
}

func fetchTokenInfoJupiter(mint string) (TokenInfo, error) {
	var res *http.Response
	var err error
	var body []byte

	// Get https data with retry
	err = retryRPC(func() error {
		res, err = http.Get("https://api.jup.ag/tokens/v1/token/" + mint)
		if err != nil {
			return fmt.Errorf("failed to send HTTP request: %v", err)
		}
		defer res.Body.Close()

		// Read the response body
		body, err = io.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %v", err)
		}

		// Check if the response contains "not found"
		if strings.Contains(string(body), "not found") {
			return fmt.Errorf("not found")
		}

		// Check for non-200 status codes
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("%s", res.Status)
		}

		return nil
	})
	if err != nil {
		return TokenInfo{}, err
	}

	// Unmarshal the JSON response
	var tokenInfo TokenInfo
	err = json.Unmarshal(body, &tokenInfo)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("failed to unmarshal JSON response: %v", err)
	}

	// Shorten long names
	if len(tokenInfo.Name) > 10 {
		tokenInfo.Name = fmt.Sprintf("%s..", tokenInfo.Name[:10])
	}

	return tokenInfo, nil
}

// Fetches token price from Jupiter exchange
func fetchTokenPriceJupiter(mint string) (float64, error) {
	var res *http.Response
	var err error

	// Get https data with retry
	err = retryRPC(func() error {
		res, err = http.Get("https://api.jup.ag/price/v2?ids=" + mint)
		if err != nil {
			return fmt.Errorf("failed to send HTTP request: %v", err)
		}
		// Check for non-200 status codes
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("%s", res.Status)
		}
		return nil
	})
	if err != nil {
		return 0, err // Return the original error
	}
	defer res.Body.Close()

	// Read the response body
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %v", err)
	}

	// Check if the response contains "null" for the mint
	if strings.Contains(string(body), `"`+mint+`":null`) {
		return 0, fmt.Errorf("price null")
	}

	// Unmarshal the JSON response
	var resp struct {
		Data map[string]struct {
			Price string `json:"price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("failed to unmarshal JSON response: %v", err)
	}

	price, _ := strconv.ParseFloat(resp.Data[mint].Price, 64)

	return price, nil
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

	log.Printf("Loaded token store from '%s'\n", FILE_TOKEN_DATA)
	return nil
}
