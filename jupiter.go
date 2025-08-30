package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TokenInfo struct {
	Address           string    `json:"address,omitempty"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	DailyVolume       float64   `json:"daily_volume,omitempty"`
	Decimals          int       `json:"decimals,omitempty"`
	FreezeAuthority   string    `json:"freeze_authority,omitempty"`
	MintAuthority     string    `json:"freeze_mint_authority,omitempty"`
	MintedAt          time.Time `json:"minted_at,omitempty"`
	Name              string    `json:"name,omitempty"`
	PermanentDelegate string    `json:"permanent_delegate,omitempty"`
	Symbol            string    `json:"symbol,omitempty"`
	Price             float64   `json:"price,omitempty"`
	Balance           float64   `json:"balance,omitempty"`
	USDValue          float64   `json:"usd_value,omitempty"`
}

// GetTokenInfo fetches token metadata from Jupiter and caches it.
func GetTokenInfo(mint string, wallet *Wallet) (TokenInfo, error) {
	// Check if the token info is already cached
	if cachedTokenInfo, exists := wallet.Token[mint]; exists {
		return cachedTokenInfo, nil
	}

	// If not cached, fetch token info from Jupiter
	tokenInfo, err := fetchTokenInfoJupiter(mint)
	if err != nil {
		return tokenInfo, err
	}

	// Cache price
	wallet.Token[mint] = tokenInfo
	return tokenInfo, err
}

// Gets any number of token prices from Jupiter.
func getTokenPrice(mints ...string) (map[string]float64, error) {
	prices := make(map[string]float64) // Final map to store all prices
	var batch []string

	for i, mint := range mints {
		batch = append(batch, mint)

		// When we have 100 mints or it's the last mint
		if (i+1)%100 == 0 || i == len(mints)-1 {
			// Join the batch into a single string
			batchStr := strings.Join(batch, ",")

			// Fetch prices for the current batch
			priceMap, err := fetchTokenPriceJupiter(batchStr)
			if err != nil {
				log.Printf("Failed to fetch prices for batch %d: %v", (i/100)+1, err)
			}
			// Merge fetched prices into the final prices map
			for mintID, price := range priceMap {
				prices[mintID] = price
			}

			// Reset the batch
			batch = nil
		}
	}

	// Return error if pricemap is empty
	if len(prices) == 0 {
		return nil, fmt.Errorf("pricemap is empty")
	}

	return prices, nil
}

// GetTokenInfoAndPrice gets token metadata and price from Jupiter.
func GetTokenInfoAndPrice(mint string, wallet *Wallet) (TokenInfo, error) {
	// Check if the token info is already cached
	if cachedTokenInfo, exists := wallet.Token[mint]; exists && cachedTokenInfo.Name != "" {
		// Fetch the price for the cached token
		usdPrice, err := fetchTokenPriceJupiter(mint)
		if err != nil {
			log.Printf("Failed to fetch price for token %s: %v", mint, err)
			cachedTokenInfo.Price = 0
			return cachedTokenInfo, err
		}

		// Update the cached token info with the new price
		cachedTokenInfo.Price = usdPrice[mint]
		return cachedTokenInfo, nil
	}

	// If not cached, fetch token info from Jupiter
	tokenInfo, err := fetchTokenInfoJupiter(mint)
	if err != nil {
		return TokenInfo{}, err
	}

	// Fetch token price using Jupiter API
	usdPrice, err := fetchTokenPriceJupiter(mint)
	if err != nil {
		wallet.Token[mint] = tokenInfo
		return tokenInfo, err
	}

	// Cache price
	tokenInfo.Price = usdPrice[mint]
	wallet.Token[mint] = tokenInfo
	return tokenInfo, err
}

// Fetches token info from Jupiter exchange
func fetchTokenInfoJupiter(mint string) (TokenInfo, error) {
	var res *http.Response
	var err error
	var body []byte

	// Get https data with retry
	err = retryRPC(func() error {
		res, err = http.Get("https://token.jup.ag/all")
		if err != nil {
			return fmt.Errorf("failed to send HTTP request: %v", err)
		}
		defer res.Body.Close()

		// Read the response body
		body, err = io.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %v", err)
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

	// Parse the response to find the specific token
	var tokens []TokenInfo
	err = json.Unmarshal(body, &tokens)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("failed to unmarshal JSON response: %v", err)
	}

	// Find the token with matching address
	for _, token := range tokens {
		if token.Address == mint {
			return token, nil
		}
	}

	return TokenInfo{}, fmt.Errorf("token not found: %s", mint)
}

// Fetches token prices from Jupiter exchange using quote API
func fetchTokenPriceJupiter(mint string) (map[string]float64, error) {
	prices := make(map[string]float64)

	// For SOL, we can get the price by quoting against USDC
	if mint == SOL {
		var res *http.Response
		var err error

		// Get quote for 1 SOL to USDC
		err = retryRPC(func() error {
			res, err = http.Get("https://quote-api.jup.ag/v6/quote?inputMint=" + SOL + "&outputMint=" + USDC + "&amount=1000000000")
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
			return nil, err
		}
		defer res.Body.Close()

		// Read the response body
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %v", err)
		}

		// Unmarshal the JSON response
		var resp struct {
			OutAmount string `json:"outAmount"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON response: %v", err)
		}

		// Convert outAmount to price (USDC has 6 decimals)
		outAmount, err := strconv.ParseFloat(resp.OutAmount, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse outAmount: %v", err)
		}

		// Price = outAmount / 10^6 (USDC decimals) / 1 SOL
		price := outAmount / 1000000.0
		prices[mint] = price
	} else {
		// For other tokens, try to get quote against USDC
		var res *http.Response
		var err error

		// Get quote for 1 token to USDC (assuming 6 decimals for most tokens)
		err = retryRPC(func() error {
			res, err = http.Get("https://quote-api.jup.ag/v6/quote?inputMint=" + mint + "&outputMint=" + USDC + "&amount=1000000")
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
			return nil, err
		}
		defer res.Body.Close()

		// Read the response body
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %v", err)
		}

		// Unmarshal the JSON response
		var resp struct {
			OutAmount string `json:"outAmount"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON response: %v", err)
		}

		// Convert outAmount to price (USDC has 6 decimals)
		outAmount, err := strconv.ParseFloat(resp.OutAmount, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse outAmount: %v", err)
		}

		// Price = outAmount / 10^6 (USDC decimals) / 1 token
		price := outAmount / 1000000.0
		prices[mint] = price
	}

	return prices, nil
}
