package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"sort"
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
	Name       string               // Wallet name
	Tokens     map[string]TokenInfo // Map of token addresses to TokenInfo
	LastUpdate time.Time            // Last update time
}

var (
	walletMap = make(map[string]*Wallet) // Map of wallet addresses to Wallet struct
	debug     = false
	showsol   = false
	mu        sync.Mutex
)

func main() {
	// Notify the channel for SIGINT (Ctrl+C) and SIGTERM (termination signal)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start a goroutine to handle the signal
	go func() {
		fmt.Printf("signal %v...\n", <-sigChan)

		// Save token data
		if err := SaveTokenData(); err != nil {
			log.Fatalf("Failed to save token store: %v", err)
		}
		os.Exit(0)
	}()

	// Application flags
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.BoolVar(&showsol, "sol", false, "Include SOL balance")
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	flag.Parse()

	if debug {
		log.Println("Debug mode is 'enabled'.")
	}

	// Load configuration
	err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load app settings: %v", err)
	}

	log.Printf("Loaded app settings from '%v'.\n", *configFile)

	// Initialize RPC client
	c := rpc.NewWithCustomRPCClient(rpc.NewWithLimiter(config.NetworkURL, 4, 1))

	// Initialize wallet map
	for _, addr := range config.Wallets {
		pubKey, err := solana.PublicKeyFromBase58(addr)
		if err != nil {
			log.Fatalf("Failed to convert wallet address to PublicKey: %v", err)
			os.Exit(1)
		}
		// Get wallet name is any
		walletname, _ := GetWalletName(pubKey, c)

		walletMap[addr] = &Wallet{
			Address: addr,
			Name:    walletname,
			Tokens:  make(map[string]TokenInfo),
		}
	}

	// Load token data
	if err := LoadTokenData(); err != nil {
		log.Fatalf("Failed to load token data: %v", err)
	}

	log.Println("Initalizing wallets and fetching token metadata.")
	log.Println("This process may take longer for wallets with a large number of tokens...")

	// Fetch initial balances
	for _, addr := range config.Wallets {
		updateWalletBalanceAndPrices(c, addr)
	}

	// Print initial balances (sorted by USDValue in descending order)
	for _, addr := range config.Wallets {
		tokenSlice := make([]TokenInfo, 0, len(walletMap[addr].Tokens))
		for _, tokenInfo := range walletMap[addr].Tokens {
			tokenSlice = append(tokenSlice, tokenInfo)
		}
		sort.Slice(tokenSlice, func(i, j int) bool {
			return tokenSlice[i].USDValue > tokenSlice[j].USDValue
		})
		log.Printf("%-10s %10s %10s$ %s\n", "SYMBOL", "BALANCE", "USD VALUE", "TOKEN MINT")
		for i, tokenInfo := range tokenSlice {
			if i >= 10 { // Limit to 10 tokens
				log.Printf("... and %d more tokens ...\n", len(walletMap[addr].Tokens)-10)
				break
			}
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
			err := updateWalletBalanceAndPrices(c, addr)
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
						log.Printf("%-10s %+10.f %+10.f$ %s <CHANGE>\n", currentToken.Symbol, balanceDiff, usdValueDiff, mint)
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

func updateWalletBalanceAndPrices(c *rpc.Client, walletAddr string) error {
	// pubKey for wallet address
	pubKey, err := solana.PublicKeyFromBase58(walletAddr)
	if err != nil {
		return fmt.Errorf("could not convert to solana.PublicKey format: %v", err)
	}

	// main tokens map
	var mints []string
	tokenMap := make(map[string]TokenInfo)

	if showsol {
		// Show SOL balance
		solBalance, err := c.GetBalance(
			context.Background(),
			pubKey,
			rpc.CommitmentConfirmed,
		)
		if err != nil {
			if debug {
				log.Printf("failed to fetch SOL balance: %v\n", err)
			}
			goto NEXT
		}
		// Convert SOL balance from lamports to SOL (1 SOL = 10^9 lamports)
		solBalanceInSOL := float64(solBalance.Value) / 1e9

		// Fetch SOL price using Jupiter API
		solPrice, err := fetchTokenPriceJupiter("So11111111111111111111111111111111111111112") // SOL mint address
		if err != nil {
			if debug {
				log.Printf("failed to fetch SOL price: %v\n", err)
			}
			goto NEXT
		}

		// Calculate SOL USD value
		solUSDValue := solBalanceInSOL * solPrice["So11111111111111111111111111111111111111112"]

		// Add SOL to the wallet map
		tokenMap["So11111111111111111111111111111111111111112"] = TokenInfo{
			Address:  "So11111111111111111111111111111111111111112",
			Symbol:   "SOL",
			Name:     "Solana",
			Balance:  solBalanceInSOL,
			USDValue: solUSDValue,
		}
		if debug {
			log.Println("Fetched SOL balance")
		}
	}

NEXT:

	start := time.Now()
	// Fetch token accounts for both Token and Token-2022 programs
	for _, program := range []solana.PublicKey{solana.TokenProgramID, solana.Token2022ProgramID} {
		// fetch token account data
		ret, err := fetchTokenAccounts(c, pubKey, program)
		if err != nil {
			log.Printf("Failed to fetch token accounts for program %s: %v", program, err)
			continue
		}

		if debug {
			log.Printf("Fetched %d token accounts for program %s", len(ret), program)
		}

		// merge tokens to the main map
		for mint, tokenProgram := range ret {
			if _, exists := skipTokens[mint]; exists {
				if debug {
					log.Printf("Skipping token %s", mint)
				}
				continue
			}
			// get token metadata
			tokenInfo, err := getTokenInfo(mint)
			if err != nil {
				if debug {
					log.Printf("Failed to fetch token metadata for %s: %v", mint, err)
				}
				skipTokens[mint] = true
				continue
			}

			// Calculate balance
			balance := tokenProgram.Balance / math.Pow10(tokenInfo.Decimals)
			tokenMap[mint] = TokenInfo{
				Address: mint,
				Balance: balance,
				Symbol:  tokenInfo.Symbol,
				Name:    tokenInfo.Name,
				Price:   tokenInfo.Price,
			}

			// add to our mints list for price fetching
			mints = append(mints, mint)
		}
	}

	// get token prices
	priceMap, err := getTokenPrice(mints...)
	if err != nil {
		log.Printf("Failed to get token prices: %v", err)
	}

	if debug {
		log.Printf("Fetched prices for %d tokens.\n", len(priceMap))
	}

	// Range over toeknMap and preform filters
	for mint, price := range priceMap {
		token, exists := tokenMap[mint]
		if !exists {
			continue
		}

		// Calculate actual balance
		usdValue := token.Balance * price
		if usdValue >= config.MinimumValue && token.Balance >= config.MinimumBalance {
			token.Price = price
			token.USDValue = usdValue
			tokenMap[mint] = token
		} else {
			delete(tokenMap, mint)
		}
	}

	// remove tokens from walletMap that we dont have in new tokensMap
	for mint := range walletMap[walletAddr].Tokens {
		// Check for removed tokens and merge new tokens
		if _, exist := tokenMap[mint]; !exist {
			delete(walletMap[walletAddr].Tokens, mint)
		}
	}

	// copy new tokensMap to main walletMap
	for mint, token := range tokenMap {
		walletMap[walletAddr].Tokens[mint] = token
	}

	// save walletMap last update time
	walletMap[walletAddr].LastUpdate = time.Now()
	log.Printf("Wallet> %s %s(%d tokens) in %v.", walletAddr, walletMap[walletAddr].Name, len(walletMap[walletAddr].Tokens), time.Since(start).Truncate(time.Second))

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

// GetWalletName resolves a wallet address to its .sol name (if any).
func GetWalletName(walletAddress solana.PublicKey, client *rpc.Client) (string, error) {
	// Derive the SNS account key for the wallet address
	snsAccount, _, err := solana.FindProgramAddress(
		[][]byte{
			[]byte("name"),
			[]byte(walletAddress.String()),
		},
		solana.MustPublicKeyFromBase58("namesLPneVptA9Z5rqUDD9tMTWEJwofgaYwp8cawRkX"),
	)
	if err != nil {
		return "", fmt.Errorf("failed to derive SNS account: %v", err)
	}

	// Fetch the SNS account data
	account, err := client.GetAccountInfo(context.TODO(), snsAccount)
	if err != nil {
		return "", fmt.Errorf("failed to fetch SNS account: %v", err)
	}

	// Check if the account exists and has data
	if account == nil || account.Value == nil || len(account.Value.Data.GetBinary()) == 0 {
		return "", nil // No .sol name found
	}

	// Decode the SNS account data to get the .sol name
	name := string(account.Value.Data.GetBinary())
	return name, nil
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
		if strings.Contains(err.Error(), "many requests") || strings.Contains(err.Error(), "429") {
			if debug {
				// Log the function name or HTTP request details
				pc, _, _, _ := runtime.Caller(1) // Get the caller's function name
				funcName := runtime.FuncForPC(pc).Name()
				log.Printf("Rate limit hit [%s]. Retrying in %v... (attempt %d/%d)", funcName, delay, i+1, config.MaxRetries)
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
