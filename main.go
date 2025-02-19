package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sort"
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
	PubKey     solana.PublicKey     // Wallet public key
	Tokens     map[string]TokenInfo // Map of token addresses to TokenInfo
	LastUpdate time.Time            // Last update time
}

var (
	walletMap = make(map[string]*Wallet) // Map of wallet addresses to Wallet struct
	signer    = true
	showsol   = false
	debug     = false

	client *rpc.Client
	mu     sync.Mutex
)

func main() {
	// Application flags
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.BoolVar(&signer, "signer", true, "Wallet owner must be signer of token changes")
	flag.BoolVar(&showsol, "sol", false, "Include SOL balance")
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	flag.Usage = func() {
		fmt.Printf(`Usage %s <REQUIRED> [OPTIONAL] ...

	Required:
	  --config <file>            Path to configuration file

	Optional:
	  --signer <true>            Shows is balance change was made by wallet owner (default: true)
	  --sol <true>               Include SOL balance (default: false)
	  --debug <false>            Debug mode (default: false)
	  -h,--help                  Show this help message

	Example:
	  %s --config wallet.config.json --signer true --debug true
	  `,
			os.Args[0], os.Args[0])
		fmt.Println()
	}
	// if flag.NArg() == 0 {
	// 	flag.Usage()
	// 	os.Exit(1)
	// }
	flag.Parse()

	if debug {
		log.Println("Debug mode is 'enabled'.")
	}

	// Notify the channel for SIGINT (Ctrl+C) and SIGTERM (termination signal)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start a goroutine to handle the signal
	go func() {
		fmt.Printf("signal %v...\n", <-sigChan)

		client.Close()
		// Save token data
		if err := SaveTokenData(); err != nil {
			log.Fatalf("Failed to save token store: %v", err)
		}
		os.Exit(0)
	}()

	// Load configuration
	err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load app settings: %v", err)
	}

	log.Printf("Loaded app settings from '%v'.\n", *configFile)

	// Initialize RPC client
	client = rpc.NewWithCustomRPCClient(rpc.NewWithLimiter(config.NetworkURL, 2, 1))

	// Initialize wallet map
	for _, addr := range config.Wallets {
		walletMap[addr] = &Wallet{
			Address: addr,
			PubKey:  solana.MustPublicKeyFromBase58(addr),
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
		updateWalletBalanceAndPrices(client, addr)
	}

	// Print initial balances (sorted by USDValue in descending order)
	log.Printf("%-10s %10s %10s$ %s\n", "SYMBOL", "BALANCE", "USD VALUE", "TOKEN MINT")
	for _, addr := range config.Wallets {
		tokenSlice := make([]TokenInfo, 0, len(walletMap[addr].Tokens))
		for _, tokenInfo := range walletMap[addr].Tokens {
			tokenSlice = append(tokenSlice, tokenInfo)
		}
		sort.Slice(tokenSlice, func(i, j int) bool {
			return tokenSlice[i].USDValue > tokenSlice[j].USDValue
		})
		for i, tokenInfo := range tokenSlice {
			if i >= 10 { // Limit to 10 tokens
				log.Printf("%s..> ... and %d more tokens ...\n", addr[:4], len(walletMap[addr].Tokens)-10)
				break
			}
			log.Printf("%s..> %-10s %10.f %10.f$ %s\n", addr[:4], tokenInfo.Symbol, tokenInfo.Balance, tokenInfo.USDValue, tokenInfo.Address)
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
			// Store changes made to tokens
			var changes = make(map[string]string)

			// Copy the current tokens for comparison
			previousTokens := make(map[string]TokenInfo)
			for k, v := range walletMap[addr].Tokens {
				previousTokens[k] = v
			}

			// Update the wallet balance
			dur := updateWalletBalanceAndPrices(client, addr)

			log.Printf("Updated> %s %s(%d tokens) in %v.", addr, walletMap[addr].Name, len(walletMap[addr].Tokens), dur)
			// Check for new tokens and balance changes
			for mint, currentToken := range walletMap[addr].Tokens {
				if previousToken, exists := previousTokens[mint]; !exists {
					log.Printf("%s..> %-10s %+10.f %+10.f$ %s <NEW>\n", addr[:4], currentToken.Symbol, currentToken.Balance, currentToken.USDValue, mint)

					// Add new token to changes
					changes[mint] = currentToken.Symbol
				} else {
					balanceDiff := currentToken.Balance - previousToken.Balance
					if math.Abs(balanceDiff) >= 0.001 {
						usdValueDiff := currentToken.USDValue - previousToken.USDValue
						log.Printf("%s..> %-10s %+10.f %+10.f$ %s <CHANGE>\n", addr[:4], currentToken.Symbol, balanceDiff, usdValueDiff, mint)

						// Add token to changes
						changes[mint] = currentToken.Symbol
					}
				}
			}

			// Check for removed tokens
			for mint, previousToken := range previousTokens {
				if _, exists := walletMap[addr].Tokens[mint]; !exists {
					log.Printf("%s..> %-10s %+10.f %+10.f$ %s <REMOVED>\n", addr[:4], previousToken.Symbol, -previousToken.Balance, -previousToken.USDValue, mint)

					// Add removed token to changes
					changes[mint] = previousToken.Symbol
				}
			}

			// Check if transacton for token change is signed by wallet owner
			// This can filter out balance changes not done by wallet owner
			if signer {
				for mint, symbol := range changes {

					isOwner, err := isWalletSignerOfTransaction(client, walletMap[addr].PubKey, mint)
					if err != nil {
						log.Printf("Failed to check lookup token change: %v", err)
						continue
					}

					if !isOwner {
						log.Printf("<%s..> %s transation WAS NOT signed by wallet!", addr[:4], symbol)

						// copytrade? make transaction
					}
				}
			}
		}
	}
}

func updateWalletBalanceAndPrices(client *rpc.Client, walletAddr string) time.Duration {
	// pubKey for wallet address
	pubKey, err := solana.PublicKeyFromBase58(walletAddr)
	if err != nil {
		return 0
	}

	// main tokens map
	var mints []string
	tokenMap := make(map[string]TokenInfo)

	if showsol {
		// Show SOL balance
		solBalance, err := client.GetBalance(
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
		ret, err := fetchTokenAccounts(client, pubKey, program)
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
				continue
			}

			// get token metadata
			tokenInfo, err := getTokenInfo(mint)
			if err != nil {
				if debug {
					log.Printf("Failed to fetch token metadata for %s: %v\n", mint, err)
					log.Printf("Added %s to skip-list!\n", mint)
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

	end := time.Since(start).Truncate(time.Second)
	return end
}

func fetchTokenAccounts(client *rpc.Client, pubKey solana.PublicKey, programID solana.PublicKey) (map[string]TokenInfo, error) {
	tokens := make(map[string]TokenInfo)
	// Fetch token accounts
	var accounts *rpc.GetTokenAccountsResult
	err := retryRPC(func() error {
		var err error
		accounts, err = client.GetTokenAccountsByOwner(
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

func isWalletSignerOfTransaction(client *rpc.Client, walletPubKey solana.PublicKey, mint string) (bool, error) {
	// Create a pointer to an integer for the Limit field
	limit := 5

	// Fetch the latest transactions for the wallet
	var txList []*rpc.TransactionSignature
	err := retryRPC(func() error {
		var err error
		txList, err = client.GetSignaturesForAddressWithOpts(
			context.Background(),
			walletPubKey,
			&rpc.GetSignaturesForAddressOpts{
				Limit: &limit, // Pass a pointer to the limit
			},
		)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("failed to fetch transaction history: %v", err)
	}

	// Iterate through the transactions
	for _, tx := range txList {
		// Fetch the transaction details
		var txDetails *rpc.GetTransactionResult
		err := retryRPC(func() error {
			var err error
			txDetails, err = client.GetTransaction(
				context.Background(),
				tx.Signature,
				&rpc.GetTransactionOpts{
					Encoding:                       solana.EncodingBase64,
					MaxSupportedTransactionVersion: new(uint64), // Support all versions
				},
			)
			return err
		})
		if err != nil {
			if debug {
				log.Printf("Failed to fetch transaction details for %s: %v", tx.Signature, err)
			}
			continue
		}

		// Decode the transaction
		var transaction solana.Transaction
		err = bin.NewBinDecoder(txDetails.Transaction.GetBinary()).Decode(&transaction)
		if err != nil {
			if debug {
				log.Printf("Failed to decode transaction %s: %v", tx.Signature, err)
			}
			continue
		}

		// Check if the wallet's public key signed the transaction
		if !transaction.IsSigner(walletPubKey) {
			continue
		}

		// Check if the transaction involves the specified mint
		if transactionInvolvesMint(&transaction, mint) {
			return true, nil
		}
	}

	return false, nil
}

func transactionInvolvesMint(transaction *solana.Transaction, mint string) bool {
	// Iterate through the accounts in the transaction to check if the mint is involved
	for _, account := range transaction.Message.AccountKeys {
		if account.String() == mint {
			return true
		}
	}
	return false
}

/*
2025/02/19 01:50:16 Wallet> 5mmGGdGcDyB14Gb2ixS54SDRrGQ9JBdhAuGMgBsWBEs3 (14 tokens) in 16s.
2025/02/19 01:50:38 Wallet> 6TuLcg1G1HuNLNRufqBeXZfPWEa4ti4ydWgnqAUGcLiC (113 tokens) in 21s.
2025/02/19 01:50:53 Wallet> Dj8MAV63ZoYGmgj5t3BQuBDK2pkJmgshmY2pPrfBHuHS (4 tokens) in 15s.
2025/02/19 01:50:53 GAP            +32047         +0$ 85uUxUmoC5AT3NNBYeDuYJgrsUpTSBK73Y68NVbEJRBj <NEW>
2025/02/19 01:52:10 Updated> 5mmGGdGcDyB14Gb2ixS54SDRrGQ9JBdhAuGMgBsWBEs3 (14 tokens) in 1s.
2025/02/19 01:52:10 Dj8MA..> GAP            +32047         +0$ 85uUxUmoC5AT3NNBYeDuYJgrsUpTSBK73Y68NVbEJRBj <NEW>
2025/02/19 01:50:16 Init 5a> 5mmGGdGcDyB14Gb2ixS54SDRrGQ9JBdhAuGMgBsWBEs3 (14 tokens) in 16s.

*/
