package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Config struct {
	Network struct {
		CustomRPC string `json:"CustomRPC"`
		CustomWS  string `json:"CustomWS"`
	} `json:"NETWORK"`
	Monitor struct {
		Wallets        []string `json:"Wallets"`
		ChangePercent  float64  `json:"ChangePercent"`
		ChangeValueUSD float64  `json:"ChangeValueUSD"`
		TokenBalance   float64  `json:"TokenBalance"`
		TokenValueUSD  float64  `json:"TokenValueUSD"`
	} `json:"MONITOR"`
	TokenFilter struct {
		IncludeTokenList []string `json:"IncludeTokenList"`
		ExcludeTokenList []string `json:"ExcludeTokenList"`
	} `json:"TOKEN_FILTER"`
	ActionWallet struct {
		PublicKey  string  `json:"PublicKey"`
		PrivateKey string  `json:"PrivateKey"`
		BuyMint    string  `json:"BuyMint"`
		SellMint   string  `json:"SellMint"`
		BuyAmount  float64 `json:"BuyAmount"`
	} `json:"ACTION_WALLET"`
}

var config Config

// loadConfig reads the configuration file and populates the Config struct
func loadConfig(filepath string) error {
	// Open the configuration file
	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open config file: %v", err)
	}
	defer file.Close()

	// Read the file content
	bytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read config file: %v", err)
	}

	// Unmarshal the JSON into the Config struct
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %v", err)
	}

	return nil
}
