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
	"github.com/gagliardetto/solana-go/rpc/ws"
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
	signer    = false
	debug     = false

	client *rpc.Client
	mu     sync.Mutex // Mutex for synchronization
)

func main() {
	// Define flags
	flag.BoolVar(&signer, "signer", false, "Only show account changes signed by wallet owner")
	flag.BoolVar(&debug, "debug", false, "Debug mode")

	// Custom usage message
	flag.Usage = func() {
		fmt.Printf(`Usage %s <FILE> [OPTIONAL] ...

Required:
	<FILE>               Path to configuration file

Optional:
	--signed bool        Only show changes that is signed by wallet (default: false)
	--debug  bool        Debug mode (default: false)
	-h,--help            Show help message

Example:
	%s wallet.config.json --signed true --debug
`,
			os.Args[0], os.Args[0])
		fmt.Println()
	}

	// Parse flags manually to allow flags after positional arguments
	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(0)
	}

	// Extract the configuration file (positional argument)
	configFile := os.Args[1]

	// Parse the remaining arguments (flags)
	err := flag.CommandLine.Parse(os.Args[2:])
	if err != nil {
		fmt.Println("Error parsing flags:", err)
		flag.Usage()
		os.Exit(1)
	}

	if debug {
		log.Println("Debug mode is 'enabled'.")
	}
	if signer {
		log.Println("Signer is enabled. Will only display token changes signed by wallet owers")
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
	err = loadConfig(configFile)
	if err != nil {
		log.Fatalf("Failed to load app settings: %v", err)
	}

	log.Printf("Loaded app settings from '%v'.\n", configFile)
	// Initialize RPC client
	client = rpc.NewWithCustomRPCClient(rpc.NewWithLimiter(config.CustomRPC, 5, 1))

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

	// Fetch and print initial balances (sorted by USDValue in descending order)
	for _, addr := range config.Wallets {
		// Update the wallet balance
		dur := updateWalletBalanceAndPrices(client, addr)
		log.Printf("Updated> %s %s(%d tokens) in %v.", addr, walletMap[addr].Name, len(walletMap[addr].Tokens), dur)

		tokenSlice := make([]TokenInfo, 0, len(walletMap[addr].Tokens))
		for _, tokenInfo := range walletMap[addr].Tokens {
			tokenSlice = append(tokenSlice, tokenInfo)
		}
		sort.Slice(tokenSlice, func(i, j int) bool {
			return tokenSlice[i].USDValue > tokenSlice[j].USDValue
		})
		for i, tokenInfo := range tokenSlice {
			if i >= 10 { // Limit to 10 tokens
				log.Printf("%s> ... and %d more tokens ...\n", addr[:4], len(walletMap[addr].Tokens)-10)
				break
			}
			log.Printf("%s> %-13s %-13.f $%-13.f %s\n", addr[:4], tokenInfo.Symbol, tokenInfo.Balance, tokenInfo.USDValue, tokenInfo.Address)

		}
	}

	// Connect to WebSocket
	wsClient, err := ws.Connect(context.Background(), rpc.MainNetBeta_WS)
	if err != nil {
		panic(err)
	}
	defer wsClient.Close()

	log.Printf("Enstablished connection to %s\n", rpc.MainNetBetaSerum_WS)
	// Monitor transactions for each wallet
	for _, wallet := range walletMap {
		go func(wallet *Wallet) {
			// Subscribe to account changes for the wallet's public key
			accountSub, err := wsClient.AccountSubscribe(
				wallet.PubKey, // solana.PublicKey
				rpc.CommitmentConfirmed,
			)
			if err != nil {
				log.Printf("Failed to subscribe to account changes for wallet %s: %v", wallet.Address, err)
				return
			}
			defer accountSub.Unsubscribe()

			// Wait for account change notifications
			for {
				_, err := accountSub.Recv(context.Background())
				if err != nil {
					log.Printf("WebSocket subscription error for account changes: %v", err)
					return
				}
				// Process the transaction concurrently
				ProcessWalletTransactinos(client, wallet.Address)
			}
		}(wallet)

		log.Printf("Subscribe to account changes for wallet %s\n", wallet.Address)
	}
	select {}
}

// ProcessRecentTransactions. FIlter and print relevant data
func ProcessWalletTransactinos(client *rpc.Client, walletAddr string) {
	// Use a goroutine to process transactions concurrently
	go func() {
		// pubKey for wallet address
		pubKey, err := solana.PublicKeyFromBase58(walletAddr)
		if err != nil {
			log.Printf("Failed to get public key for wallet %s: %v", walletAddr, err)
			return
		}

		// Calculate limit in one line
		limit := 1

		// Fetch recent transactions for the wallet
		var txList []*rpc.TransactionSignature
		err = retryRPC(func() error {
			var err error
			txList, err = client.GetSignaturesForAddressWithOpts(
				context.TODO(),
				pubKey,
				&rpc.GetSignaturesForAddressOpts{
					Limit: &limit,
				},
			)
			return err
		})
		if err != nil {
			log.Printf("Failed to fetch transactions for wallet %s: %v", walletAddr, err)
			return
		}

		// Iterate through the transactions
		for _, sig := range txList {
			// Print transaction time
			if debug {
				log.Printf("%s> Tx: %s\n", walletAddr[:4], sig.Signature.String())
			}

			// Fetch the transaction details
			var tx *rpc.GetTransactionResult
			err := retryRPC(func() error {
				var err error
				tx, err = client.GetTransaction(
					context.TODO(),
					sig.Signature,
					&rpc.GetTransactionOpts{
						Encoding:                       solana.EncodingBase64,
						MaxSupportedTransactionVersion: new(uint64), // Support all versions
					},
				)
				return err
			})
			if err != nil {
				log.Printf("Failed to fetch transaction %s: %v", sig.Signature, err)
				continue
			}

			var transaction solana.Transaction
			err = bin.NewBinDecoder(tx.Transaction.GetBinary()).Decode(&transaction)
			if err != nil {
				log.Printf("Failed to decode transaction %s: %v", sig.Signature.String(), err)
				continue
			}

			// Check if the wallet's public key signed the transaction
			if !transaction.IsSigner(pubKey) {
				if debug {
					log.Printf("%s> Transaction is NOT signed by this wallet!\n", walletAddr[:4])
				}
				if signer {
					continue
				}
			}

			// Extract token balances before and after the transaction
			meta := tx.Meta
			if meta == nil {
				if debug {
					log.Printf("No metadata found for transaction %s\n", sig.Signature)
				}
				continue
			}

			// Lock the mutex to protect shared resources
			mu.Lock()

			for i, preBalance := range meta.PreTokenBalances {
				if !includeTokenFilter(preBalance.Mint.String()) {
					if debug {
						log.Printf("toeken mint %s filtered by Includefilter\n", preBalance.Mint.String())
					}
					continue
				}

				// Ensure postBalance exists
				if i >= len(meta.PostTokenBalances) {
					if debug {
						log.Printf("No post balance found for token account %v\n", preBalance.AccountIndex)
					}
					continue
				}
				postBalance := meta.PostTokenBalances[i]

				// Check if the preBalance or postBalance is owned by us
				isPreBalanceOwnedByUs := *preBalance.Owner == pubKey
				isPostBalanceOwnedByUs := *postBalance.Owner == pubKey

				// Skip if neither preBalance nor postBalance is owned by us
				if !isPreBalanceOwnedByUs && !isPostBalanceOwnedByUs {
					continue
				}

				// Calculate balances
				balanceBefore := 0.
				if preBalance.UiTokenAmount.UiAmount != nil {
					balanceBefore = *preBalance.UiTokenAmount.UiAmount
				}
				balanceAfter := 0.
				if postBalance.UiTokenAmount.UiAmount != nil {
					balanceAfter = *postBalance.UiTokenAmount.UiAmount
				}

				// Get TokenData from Jupiter
				tokenInfo, err := getTokenInfo(preBalance.Mint.String())
				if err != nil {
					log.Printf("Failed to fetch token metadata for %s: %v\n", preBalance.Mint.String(), err)
					continue
				}

				// Get Token Price from Jupiter
				priceMap, err := getTokenPrice(preBalance.Mint.String())
				if err != nil {
					log.Printf("Failed to get token prices: %v", err)
					continue
				}
				price := priceMap[preBalance.Mint.String()]

				// Calculate balance change
				balanceChange := balanceAfter - balanceBefore
				balanceChangeUSD := balanceChange * price

				// Log the transaction details
				var action string
				if balanceBefore == 0 {
					action = "BUY"
				} else if balanceAfter == 0 {
					action = "SELL"
				} else {
					action = "UPDATE"
				}

				log.Printf("%4s> %-+6s %-12v %-+14f $%-+14f %-s\n", walletAddr[:4], action, tokenInfo.Symbol, balanceChange, balanceChangeUSD, preBalance.Mint.String())
			}

			// Unlock the mutex after processing the transaction
			mu.Unlock()
		}
	}()
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

	for {
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
			break
		}

		// Convert SOL balance from lamports to SOL (1 SOL = 10^9 lamports)
		solBalanceInSOL := float64(solBalance.Value) / 1e9

		// Fetch SOL price using Jupiter API
		solPrice, err := fetchTokenPriceJupiter("So11111111111111111111111111111111111111112") // SOL mint address
		if err != nil {
			if debug {
				log.Printf("failed to fetch SOL price: %v\n", err)
			}
			break
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
		break
	}

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
		token.Price = price
		token.USDValue = usdValue
		tokenMap[mint] = token
	}

	// Remove tokens from walletMap that does not exist in tokensMap
	for mint := range walletMap[walletAddr].Tokens {
		if _, exist := tokenMap[mint]; !exist {
			delete(walletMap[walletAddr].Tokens, mint)
		}
	}

	// Copy tokens from tokensMap to the walletMap
	for mint, token := range tokenMap {
		walletMap[walletAddr].Tokens[mint] = token
	}

	// Save walletMap last update time
	walletMap[walletAddr].LastUpdate = time.Now()

	return time.Since(start).Truncate(time.Second)
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
