package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"github.com/mr-tron/base58"
)

type Wallet struct {
	Address string               // Wallet address
	Name    string               // Wallet name
	PubKey  solana.PublicKey     // Wallet public key
	Token   map[string]TokenInfo // Map of token addresses to TokenInfo
	skip    map[string]TokenInfo // Map of tokens to skip (no pricedata)

	// dont save tokenData, save wallet as separate file instead
	// integrate Skip in wallet instead of global skip
}

var (
	walletMap = make(map[string]*Wallet) // Map of wallet addresses to Wallet struct

	showBalance      = false
	showTransactions = false
	verbose          = false
	client           *rpc.Client
	mu               sync.Mutex // Mutex for synchronization
	wg               sync.WaitGroup
)

func main() {
	// Define flags
	flag.BoolVar(&showBalance, "balance", false, "Show all token balances on program start.")
	flag.BoolVar(&showTransactions, "all", false, "Show all transactions associaded with account.")
	flag.BoolVar(&verbose, "verbose", false, "Verbose mode. Show all messages")

	// Custom usage message
	flag.Usage = func() {
		fmt.Printf(`Usage %s <FILE> [OPTIONAL] ...

Required:
    <FILE>               Path to app configuration file

Optional:
    --balance bool       Show token balance on program start (default: false)
    --all bool           Show all token transactions accociated with account wallet (default: false)
    --verbose  bool      Show all messages (default: false)
    --help,-h            Show this help message


Example:
    %s wallet.config.json --all --balance -verbose
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

	if verbose {
		log.Println("Verbose is 'enabled'.")
	}
	if showTransactions {
		log.Println("Showing all transactinos accosiated with account wallet.")
	} else {
		log.Println("Showing transactions signed by account wallet.")
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
	client = rpc.NewWithCustomRPCClient(rpc.NewWithLimiter(config.CustomRPC, 3, 1))

	// Initialize wallet map
	for _, addr := range config.Wallets {
		walletMap[addr] = &Wallet{
			Address: addr,
			PubKey:  solana.MustPublicKeyFromBase58(addr),
			Token:   make(map[string]TokenInfo),
		}
	}

	// Load token data
	if err := LoadTokenData(); err != nil {
		log.Fatalf("Failed to load token data: %v", err)
	}

	if showBalance {
		//Fetch and print token account balance (sorted by USDValue in descending order)
		log.Printf("Downloading token metadata for %d wallet accounts.\n", len(walletMap))
		for _, addr := range config.Wallets {
			// Update wallet balances and token metadata
			dur := updateWalletBalanceAndPrices(client, addr)
			log.Printf("Updated %s %s(%d tokens) in %v.", addr, walletMap[addr].Name, len(walletMap[addr].Token), dur)

			tokenSlice := make([]TokenInfo, 0, len(walletMap[addr].Token))
			for _, tokenInfo := range walletMap[addr].Token {
				tokenSlice = append(tokenSlice, tokenInfo)
			}
			sort.Slice(tokenSlice, func(i, j int) bool {
				return tokenSlice[i].USDValue > tokenSlice[j].USDValue
			})
			for _, tokenInfo := range tokenSlice {
				log.Printf("%s> %-13s %-13f $%-13.f %45s\n", addr[:4], tokenInfo.Symbol, tokenInfo.Balance, tokenInfo.USDValue, tokenInfo.Address)
			}
		}
	}

	// Connect to WebSocket
	wsClient, err := ws.Connect(context.Background(), rpc.MainNetBeta_WS)
	if err != nil {
		panic(err)
	}
	defer wsClient.Close()

	log.Printf("Established connection to %s.\n", rpc.MainNetBeta_WS)
	// Monitor transactions for each wallet

	ctx := context.Background()
	for _, wallet := range walletMap {
		wg.Add(1)
		go func(wallet *Wallet) {
			defer wg.Done()

			// Subscribe to log events that mentioning wallet
			sub, err := wsClient.LogsSubscribeMentions(
				wallet.PubKey,
				rpc.CommitmentFinalized,
			)
			if err != nil {
				log.Fatalf("Failed to subscribe to logs: %v", err)
			}
			defer sub.Unsubscribe()
			log.Printf("Subscribed to transaction logs for wallet account %s\n", wallet.Address)

			for {
				// Receive log notification
				logs, err := sub.Recv(ctx)
				if err != nil {
					log.Fatalf("Failed to receive log notification: %v", err)
				}

				// Skip if transaction failed
				if logs.Value.Err != nil {
					continue
				}

				// Fix this, we want to be able to filter out before RPC call
				// transactions that is accociated with spam token accounts like
				// flip.gg, etc.
				if isLogAssociatedWithIgnoredAccounts(logs, ignoreAccounts) {
					log.Println("FILTERED OUT FLIP.GG !!!")
					continue // Skip this log
				}
				// Extract the transaction signature from the logs
				go processTransaction(ctx, client, wallet.Address, logs.Value.Signature)

			}

		}(wallet)
	}
	wg.Wait()
}

// List of accounts to ignore
var ignoreAccounts = map[string]bool{
	"Habp5bncMSsBC3vkChyebepym5dcTNRYeg2LVG464E96": true, // Flip.gg
}

func extractAccountsFromLogs(logs []string) []string {
	var accounts []string
	re := regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`) // Solana account address pattern

	for _, logEntry := range logs {
		decodedLogEntry, err := base58.Decode(logEntry)
		if err != nil {
			continue // Skip if decoding fails
		}

		matches := re.FindAllString(string(decodedLogEntry), -1)
		accounts = append(accounts, matches...)
	}

	return accounts
}

// Helper function to check if a log is associated with ignored accounts
func isLogAssociatedWithIgnoredAccounts(logResult *ws.LogResult, ignoreAccounts map[string]bool) bool {
	// Extract account addresses from the logs
	accounts := extractAccountsFromLogs(logResult.Value.Logs)

	// Check if any of the extracted accounts are in the ignore list
	for _, account := range accounts {
		if ignoreAccounts[account] {
			return true
		}
	}
	return false
}

// processTransaction is processing transaction.
func processTransaction(ctx context.Context, client *rpc.Client, walletAddr string, sig solana.Signature) {
	// pubKey for wallet address
	pubKey, err := solana.PublicKeyFromBase58(walletAddr)
	if err != nil {
		log.Fatalf("Failed to get public key for wallet %s: %v", walletAddr, err)
	}

	// Print transaction ID
	if verbose {
		log.Printf("%s> Tx: %s\n", walletAddr[:4], sig.String())
	}

	// Get transaction details
	var tx *rpc.GetTransactionResult
	err = retryRPC(func() error {
		var err error
		tx, err = client.GetTransaction(
			ctx,
			sig,
			&rpc.GetTransactionOpts{
				Encoding:                       solana.EncodingBase64,
				MaxSupportedTransactionVersion: new(uint64), // Support all versions
			},
		)
		// Wrap the error to include "429"
		if err != nil && strings.Contains(err.Error(), "not found") {
			err = fmt.Errorf("fakeing 429. we need retry: %w", err)
		}
		return err
	})
	if err != nil {
		log.Printf("Failed to fetch transaction %s: %v", sig, err)
		return
	}

	var transaction solana.Transaction
	err = bin.NewBinDecoder(tx.Transaction.GetBinary()).Decode(&transaction)
	if err != nil {
		log.Printf("Failed to decode transaction %s: %v", sig.String(), err)
		return
	}

	// Check if the wallet account signed this transaction
	if !transaction.IsSigner(pubKey) {
		if verbose {
			log.Printf("%s> Transaction is not signed by this wallet account!\n", walletAddr[:4])
		}
		if !showTransactions {
			return
		}
	}

	// Extract token balances before and after the transaction
	meta := tx.Meta
	if meta == nil {
		if verbose {
			log.Printf("No metadata found for transaction %s\n", sig.String())
		}
		return
	}

	// Lock the mutex to protect shared resources
	mu.Lock()

	for i, preBalance := range meta.PreTokenBalances {
		// Token filter
		if !includeTokenFilter(preBalance.Mint.String()) {
			if verbose {
				log.Printf("Token mint %s filtered by Tokenfilter\n", preBalance.Mint.String())
			}
			continue
		}

		// Ensure postBalance exists
		if i >= len(meta.PostTokenBalances) {
			if verbose {
				log.Printf("No post balance found for token account %v\n", preBalance.AccountIndex)
			}
			continue
		}

		var action string
		// Check if this is a token swap
		for j, otherPreBalance := range meta.PreTokenBalances {
			if j == i {
				continue // Skip the same token
			}

			// Ensure other postBalance exists
			if j >= len(meta.PostTokenBalances) {
				continue
			}
			if preBalance.Mint.String() != otherPreBalance.Mint.String() {
				action = "SWAP"
			}
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
		percentChange := balanceChange * 100 / balanceAfter

		// Log the transaction details
		if balanceBefore == 0 {
			action = "NEW"
		} else if balanceAfter == 0 {
			action = "SOLD ALL"
		}

		var symbol string
		var am string
		var usd string
		// Symbol in green color if FreezAuthority and MintAuthority is disabled else in red
		if tokenInfo.FreezeAuthority != "" || tokenInfo.MintAuthority != "" {
			symbol = fmt.Sprintf("\033[31m%-12v\033[0m", tokenInfo.Symbol)
		} else {
			symbol = fmt.Sprintf("%-12v", tokenInfo.Symbol)
		}
		// Amounts in green if its a positive value or else in red
		if balanceChange > 0 {
			am = fmt.Sprintf("\033[32m%-+14f\033[0m", balanceChange)
			usd = fmt.Sprintf("\033[32m%-+14.f\033[0m", balanceChangeUSD)
		} else {
			am = fmt.Sprintf("\033[31m%-+14f\033[0m", balanceChange)
			usd = fmt.Sprintf("\033[31m%-+14.f\033[0m", balanceChangeUSD)
		}

		// Pretty account changes
		if math.Abs(percentChange) >= config.ChangePercent &&
			math.Abs(balanceChangeUSD) >= config.ChangeValueUSD {
			log.Printf("%4s> %s %s $%s %-46s %-6s\n", walletAddr[:4], symbol, am, usd, preBalance.Mint.String(), action)
		}
	}

	// Unlock the mutex after processing the transaction
	mu.Unlock()
}

func updateWalletBalanceAndPrices(client *rpc.Client, walletAddr string) time.Duration {
	// pubKey for wallet address
	pubKey, err := solana.PublicKeyFromBase58(walletAddr)
	if err != nil {
		log.Fatalf("Failed to get public key for wallet %s: %v", walletAddr, err)
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
			if verbose {
				log.Printf("failed to fetch SOL balance: %v\n", err)
			}
			break
		}

		// Convert SOL balance from lamports to SOL (1 SOL = 10^9 lamports)
		solBalanceInSOL := float64(solBalance.Value) / 1e9

		// Fetch SOL price using Jupiter API
		solPrice, err := fetchTokenPriceJupiter("So11111111111111111111111111111111111111112") // SOL mint address
		if err != nil {
			if verbose {
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
		ret, err := getTokenAccounts(client, pubKey, program)
		if err != nil {
			log.Printf("Failed to fetch token accounts for program %s: %v", program, err)
			continue
		}

		if verbose {
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
				if verbose {
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
	for mint := range walletMap[walletAddr].Token {
		if _, exist := tokenMap[mint]; !exist {
			delete(walletMap[walletAddr].Token, mint)
		}
	}

	// Copy tokens from tokensMap to the walletMap
	for mint, token := range tokenMap {
		walletMap[walletAddr].Token[mint] = token
	}

	return time.Since(start).Truncate(time.Second)
}

func getTokenAccounts(client *rpc.Client, pubKey solana.PublicKey, programID solana.PublicKey) (map[string]TokenInfo, error) {
	tm := make(map[string]TokenInfo)
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
		return tm, fmt.Errorf("failed to fetch token accounts for program %s: %v", programID, err)
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
				tm[mint] = TokenInfo{
					Address: mint,
					Balance: float64(tokenAccount.Amount),
				}
			}
		}
	}

	return tm, nil
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
