package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Config struct {
	CustomRPC      string   `json:"CustomRPC"`
	CustomWS       string   `json:"CustomWS"`
	Wallets        []string `json:"Wallets"`
	ChangePercent  float64  `json:"ChangePercent"`
	ChangeValueUSD float64  `json:"ChangeValueUSD"`
	IncludeTokens  []string `json:"IncludeTokenList"`
	ExcludeTokens  []string `json:"ExcludeTokenList"`
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
