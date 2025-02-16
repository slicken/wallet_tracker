package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

type Wallet struct {
	Address    string               // Wallet address
	PK         solana.PublicKey     // Solana PublicKey format
	Tokens     map[string]TokenInfo // Map of token addresses to TokenInfo
	LastUpdate time.Time            // Last update time
}

var (
	walletMap  = make(map[string]*Wallet) // Map of wallet addresses to Wallet struct
	fetchPrice = false
	debug      = false
	mu         sync.Mutex
)

func main() {
	// Notify the channel for SIGINT (Ctrl+C) and SIGTERM (termination signal)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start a goroutine to handle the signal
	go func() {
		<-sigChan
		fmt.Printf("\nExiting...\n")

		// Save token data
		if err := SaveTokenData(); err != nil {
			log.Fatalf("Failed to save jupiterToken data : %v", err)
		}
		os.Exit(0)
	}()

	// Application flags
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	flag.BoolVar(&fetchPrice, "price", false, "Fetch prices (needed to calculate USD value)")
	flag.Parse()

	if debug {
		log.Println("Debug mode is enabled")
	}

	log.Printf("Loading app settings from '%v'...\n", *configFile)

	// Load configuration
	err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load app settings: %v", err)
	}

	// Initialize wallet map
	for _, addr := range config.Wallets {
		pk, err := solana.PublicKeyFromBase58(addr)
		if err != nil {
			log.Fatalf("Failed to convert wallet address %s to PublicKey: %v", addr, err)
		}

		walletMap[addr] = &Wallet{
			Address: addr,
			PK:      pk,
			Tokens:  make(map[string]TokenInfo),
		}
	}

	// Load token data
	if err := LoadTokenData(); err != nil {
		log.Fatalf("Failed to load jupiterTokenData: %v", err)
	}

	if fetchPrice {
		log.Println("Fetching prices may take some time for wallet with many tokens...")
	}

	// Fetch initial balances
	c := rpc.NewWithCustomRPCClient(rpc.NewWithLimiter(config.NetworkURL, 4, 1))
	for _, addr := range config.Wallets {
		updateTokens(c, addr)
	}

	// Print initial balances
	for _, addr := range config.Wallets {
		for _, tokenInfo := range walletMap[addr].Tokens {
			log.Printf("%-10s %10.f %10.f$ %s\n", tokenInfo.Symbol, tokenInfo.Balance, tokenInfo.USDValue, tokenInfo.Address)
		}
	}

	// Parse the update interval from config
	interval, _ := time.ParseDuration(config.UpdateInterval)

	// Start periodic balance checking
	log.Printf("Scanning wallet balances every %v...\n", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		for _, addr := range config.Wallets {
			// Copy the current tokens for comparison
			previousTokens := make(map[string]TokenInfo)
			for k, v := range walletMap[addr].Tokens {
				previousTokens[k] = v
			}

			// Update the wallet balance
			err := updateTokens(c, addr)
			if err != nil {
				log.Printf("Failed to update wallet balance for %s: %v", addr, err)
				continue
			}

			// Check for new tokens and balance changes
			for mint, currentToken := range walletMap[addr].Tokens {
				if previousToken, exists := previousTokens[mint]; !exists {
					log.Printf("%-10s %+10.f %+10.f$ %s <NEW>\n", currentToken.Symbol, currentToken.Balance, currentToken.USDValue, mint)
				} else {
					balanceDiff := currentToken.Balance - previousToken.Balance
					if math.Abs(balanceDiff) >= 0.001 {
						usdValueDiff := currentToken.USDValue - previousToken.USDValue
						log.Printf("%-10s %10.f (%+10.f) %10.f (%+10.f$) %s\n", currentToken.Symbol, currentToken.Balance, balanceDiff, currentToken.USDValue, usdValueDiff, mint)
					}
				}
			}

			// Check for removed tokens
			for mint, previousToken := range previousTokens {
				if _, exists := walletMap[addr].Tokens[mint]; !exists {
					log.Printf("%-10s %10.f %10.f$ %s <REMOVED>\n", previousToken.Symbol, -previousToken.Balance, -previousToken.USDValue, mint)
				}
			}
		}
	}
}

func updateTokens(c *rpc.Client, walletAddr string) error {
	// pubKey for wallet address
	pubKey, err := solana.PublicKeyFromBase58(walletAddr)
	if err != nil {
		return fmt.Errorf("could not convert to solana.PublicKey format: %v", err)
	}

	// main tokens map
	tokensMap := make(map[string]TokenInfo)

	start := time.Now()
	// Fetch token accounts for both Token and Token-2022 programs
	for _, program := range []solana.PublicKey{solana.TokenProgramID, solana.Token2022ProgramID} {
		// fetch token account data
		ret, err := fetchTokenAccounts(c, pubKey, program)
		if err != nil {
			log.Printf("Failed to fetch token accounts for program %s: %v", program, err)
			continue
		}

		// merge tokens to the main map
		for mint, tokenProgram := range ret {
			if _, exists := skipTokens[mint]; exists {
				if debug {
					log.Printf("Skipping token %s", mint)
				}
				continue
			}
			// fetch new token metadata
			tokenInfo, err := fetchTokenMetadata(mint, fetchPrice)
			if err != nil {
				if debug {
					log.Printf("Failed to fetch token metadata for %s: %v", mint, err)
				}
				skipTokens[mint] = true
				continue
			}
			if debug {
				fmt.Println(">", tokenInfo.Name, tokenInfo.Price)
			}

			// Calculate actual balance
			balance := tokenProgram.Balance / math.Pow10(tokenInfo.Decimals)
			if tokenInfo.Price <= 0 {
				// If we dont have price, we will only filter by balance
				if balance >= config.MinimumBalance {
					tokensMap[mint] = TokenInfo{
						Address: mint,
						Balance: balance,
						Symbol:  tokenInfo.Symbol,
						Name:    tokenInfo.Name,
						Price:   tokenInfo.Price,
					}
				}
			} else {
				// If we have price we can also filter by USD value
				usdValue := balance * tokenInfo.Price
				if usdValue >= config.MinimumValue && balance >= config.MinimumBalance {
					tokensMap[mint] = TokenInfo{
						Address:  mint,
						Balance:  balance,
						Symbol:   tokenInfo.Symbol,
						Name:     tokenInfo.Name,
						Price:    tokenInfo.Price,
						USDValue: usdValue,
					}
				}
			}
		}
	}

	// remove tokens from walletMap that we dont have in new tokensMap
	for mint := range walletMap[walletAddr].Tokens {
		// Check for removed tokens and merge new tokens
		if _, exist := tokensMap[mint]; !exist {
			delete(walletMap[walletAddr].Tokens, mint)
		}
	}

	// copy new tokensMap to main walletMap
	for mint, token := range tokensMap {
		walletMap[walletAddr].Tokens[mint] = token
	}

	walletMap[walletAddr].LastUpdate = time.Now()
	// log.Printf("Address %s (%d tokens) fetched in %.2f seconds", walletAddr, len(walletMap[walletAddr].Tokens), time.Now().Sub(start).Seconds())
	log.Printf("Address %s (%d tokens) fetched in %v", walletAddr, len(walletMap[walletAddr].Tokens), time.Since(start).Truncate(time.Second))

	return nil
}

func fetchTokenAccounts(c *rpc.Client, pubKey solana.PublicKey, programID solana.PublicKey) (map[string]TokenInfo, error) {
	tokens := make(map[string]TokenInfo)
	// Fetch token accounts
	var accounts *rpc.GetTokenAccountsResult
	err := retryRPC(func() error {
		var err error
		accounts, err = c.GetTokenAccountsByOwner(
			context.Background(),
			pubKey,
			&rpc.GetTokenAccountsConfig{
				ProgramId: programID.ToPointer(),
			},
			&rpc.GetTokenAccountsOpts{
				Encoding: solana.EncodingBase64,
			},
		)
		return err
	})
	if err != nil {
		return tokens, fmt.Errorf("failed to fetch token accounts for program %s: %v", programID, err)
	}
	// Process token accounts
	for _, account := range accounts.Value {
		var tokenAccount token.Account
		err = bin.NewBinDecoder(account.Account.Data.GetBinary()).Decode(&tokenAccount)
		if err != nil {
			log.Printf("warning: failed to decode token account: %v", err)
			continue
		}

		// Only include accounts with positive balance
		if tokenAccount.Amount > 0 {
			mint := tokenAccount.Mint.String()

			// Check if the token should be included/excluded based on configuration
			if includeTokenFilter(mint) {
				tokens[mint] = TokenInfo{
					Address: mint,
					Balance: float64(tokenAccount.Amount),
				}
			}
		}
	}

	return tokens, nil
}

// includeTokenFilter checks if a token should be included based on the configuration
func includeTokenFilter(mint string) bool {
	// If include_tokens is not empty, only include tokens in the list
	if len(config.IncludeTokens) > 0 {
		for _, includedMint := range config.IncludeTokens {
			if includedMint == mint {
				return true
			}
		}
		return false
	}

	// If exclude_tokens is not empty, exclude tokens in the list
	for _, excludedMint := range config.ExcludeTokens {
		if excludedMint == mint {
			return false
		}
	}

	// Otherwise, include the token
	return true
}

// retryRPC retries an RPC call with exponential backoff on rate limit errors
func retryRPC(fn func() error) error {
	delay := 5 * time.Second

	var err error
	for i := 0; i < config.MaxRetries; i++ {
		if err = fn(); err == nil {
			return nil // Success, exit
		}

		// Check if the error is a rate limit error (HTTP 429)
		if strings.Contains(err.Error(), "too many requests") || strings.Contains(err.Error(), "429") {
			if debug {
				log.Printf("Rate limit hit. Retrying in %v... (attempt %d/%d)", delay, i+1, config.MaxRetries)
			}
			time.Sleep(delay)
			delay *= 2
			if delay > 15*time.Second {
				delay = 15 * time.Second
			}
			continue
		}

		// If it's not a rate limit error, return the error
		return err
	}

	return err
}
