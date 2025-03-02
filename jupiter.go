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

	return tokenInfo, nil
}

// Fetches token prices from Jupiter exchange
func fetchTokenPriceJupiter(mint string) (map[string]float64, error) {
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
		return nil, err // Return the original error
	}
	defer res.Body.Close()

	// Read the response body
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Unmarshal the JSON response
	var resp struct {
		Data map[string]struct {
			Price string `json:"price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %v", err)
	}

	// Create a map to store the prices
	prices := make(map[string]float64)

	// Iterate through the response data and populate the map
	for mintID, data := range resp.Data {
		// Skip if the price is null or empty
		if data.Price == "" || data.Price == "null" {
			continue
		}
		price, err := strconv.ParseFloat(data.Price, 64)
		if err != nil {
			continue
		}
		prices[mintID] = price
	}

	return prices, nil
}
