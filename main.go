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

	showBalance = false
	copyTrade   = false
	verbose     = false
	client      *rpc.Client
	mu          sync.Mutex // Mutex for synchronization
	wg          sync.WaitGroup
)

func main() {
	// Define flags
	flag.BoolVar(&copyTrade, "copytrade", false, "Buy and sell token swaps signed by wallets.")
	flag.BoolVar(&showBalance, "balance", false, "Show all token balances on program start.")
	flag.BoolVar(&verbose, "verbose", false, "Verbose mode. Show all messages.")

	// Custom usage message
	flag.Usage = func() {
		fmt.Printf(`Usage %s <FILE> [OPTIONAL] ...

Required:
    <FILE>               Path to app configuration file

Optional:
    --copytrade bool     Buy and sell token swaps signed by wallets (default: false).
    --balance bool       Show token balance on program start (default: false).
    --verbose bool       Show all messages (default: false).
    --help,-h            Show this help message.


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
	if copyTrade {
		log.Println("Copytrade is 'enabled'.")
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
	networkRPC := rpc.MainNetBeta_RPC
	if config.Network.CustomRPC != "" {
		networkRPC = config.Network.CustomRPC
	}
	client = rpc.NewWithCustomRPCClient(rpc.NewWithLimiter(networkRPC, 10, 10))

	// Initialize wallet map
	for _, addr := range config.Monitor.Wallets {
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
		for _, addr := range config.Monitor.Wallets {
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

	// ----------------------

	_, err = swapJupiter(context.TODO(), client, config.ActionWallet.BuyMint, USDC, 100, 0, 0)
	if err != nil {
		log.Printf("Failed to buy token: %v", err)
	}
	os.Exit(0)
	// Initialize WS client
	networkWS := rpc.MainNetBeta_WS
	if config.Network.CustomWS != "" {
		networkWS = config.Network.CustomWS
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		// Connect to WebSocket
		var wsClient *ws.Client
		err := retryRPC(func() error {
			var err error
			wsClient, err = ws.Connect(ctx, networkWS)
			return err
		})
		if err != nil {
			log.Printf("Failed to connect to WebSocket: %v", err)
			continue
		}
		defer wsClient.Close()

		log.Printf("Established connection to %s.\n", rpc.MainNetBeta_WS)

		// Monitor transactions for each wallet
		for _, wallet := range walletMap {
			wg.Add(1)
			go func(wallet *Wallet) {
				defer wg.Done()

				for {
					// Subscribe to log events that mention the wallet
					var sub *ws.LogSubscription
					err := retryRPC(func() error {
						var err error
						sub, err = wsClient.LogsSubscribeMentions(
							wallet.PubKey,
							rpc.CommitmentFinalized,
						)
						return err
					})
					if err != nil {
						log.Printf("Failed to subscribe to logs for wallet: %v\n", err)
						return
					}
					defer sub.Unsubscribe()
					log.Printf("Subscribed to transaction logs for wallet account %s\n", wallet.Address)

					for {
						// Receive log notification
						logs, err := sub.Recv(ctx)
						if err != nil {
							log.Printf("Failed to receive log notification: %v. Reconnecting...\n", err)
							break // Exit inner loop to reconnect
						}

						// Skip if transaction failed
						if logs.Value.Err != nil {
							continue
						}

						// Extract the transaction signature from the logs
						go processTransaction(ctx, client, wallet.Address, logs.Value.Signature)
					}
				}
			}(wallet)
		}

		wg.Wait()

		// If we reach here, the connection was lost, and we need to reconnect
		log.Println("WebSocket connection lost. Reconnecting...")
	}
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
		return
	}

	// Extract token balances before and after the transaction
	meta := tx.Meta
	if meta == nil {
		if verbose {
			log.Printf("No metadata found for transaction %s\n", sig.String())
		}
		return
	}
	// Check native SOL balance change
	solBalanceBefore := int64(meta.PreBalances[0])                      // Convert to signed int64
	solBalanceAfter := int64(meta.PostBalances[0])                      // Convert to signed int64
	solBalanceChange := float64(solBalanceAfter-solBalanceBefore) / 1e9 // Convert lamports to SOL

	// Get transaction fee in SOL
	txFee := float64(meta.Fee) / 1e9

	if solBalanceChange != 0 {
		solPrice := tokenData[SOL].Price
		solBalanceChangeUSD := solBalanceChange * solPrice

		var am string
		var usd string
		if solBalanceChange > 0 {
			am = fmt.Sprintf("\033[32m%-+14f\033[0m", solBalanceChange)
			usd = fmt.Sprintf("\033[32m%-+14.f\033[0m", solBalanceChangeUSD)
		} else if solBalanceChange < 0 {
			am = fmt.Sprintf("\033[31m%-+14f\033[0m", solBalanceChange)
			usd = fmt.Sprintf("\033[31m%-+14.f\033[0m", solBalanceChangeUSD)
		}

		if math.Abs(solBalanceChange+txFee) > 0.000001 {
			log.Printf("%4s> %-12s %s $%s %-46s %-6s\n", walletAddr[:4], "SOL", am, usd, SOL, "SWAP")
		}
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
		tokenInfo, err := GetTokenInfo(preBalance.Mint.String())
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
		if math.Abs(percentChange) >= config.Monitor.ChangePercent &&
			math.Abs(balanceChangeUSD) >= config.Monitor.ChangeValueUSD {
			log.Printf("%4s> %s %s $%s %-46s %-6s\n", walletAddr[:4], symbol, am, usd, preBalance.Mint.String(), action)

			// Copy trade
			if copyTrade {
				mint := preBalance.Mint.String()

				if balanceChange > 0 && mint != SOL && mint != USDC && mint != USDT && mint != config.ActionWallet.SellMint {
					// Buy token
					var spendSym string
					buyToken, ok := tokenData[config.ActionWallet.BuyMint]
					if !ok {
						spendSym = fmt.Sprintf("%s..", config.ActionWallet.BuyMint[:4])
					} else {
						spendSym = buyToken.Symbol
					}
					log.Printf("Swapping %f %s -> %s\n", config.ActionWallet.BuyAmount, spendSym, tokenInfo.Symbol)
					buyLamport := UiToLamport(config.ActionWallet.BuyAmount, config.ActionWallet.BuyMint)

					amount, err := swapJupiter(ctx, client, config.ActionWallet.BuyMint, mint, buyLamport, 0, 0)
					if err != nil {
						log.Printf("Failed to buy token: %v", err)
					}
					addPosition(mint, amount)
					if verbose {
						log.Printf("Added position %s %v\n", tokenData[mint].Symbol, LamportToUi(amount, mint))
					}
				} else if balanceChange < 0 && getPositionAmount(mint) > 0 {
					// Sell token with retry logic
					var sellSym string
					sellToken, ok := tokenData[config.ActionWallet.SellMint]
					if !ok {
						sellSym = fmt.Sprintf("%s..", config.ActionWallet.SellMint[:4])
					} else {
						sellSym = sellToken.Symbol
					}

					sellBalance, err := getTokenBalance(client, config.ActionWallet.PublicKey, mint)
					if err != nil {
						log.Printf("Failed to get token balance: %v", err)
						continue
					}
					sellBalanceLamp := UiToLamport(sellBalance, mint)

					// sellLamport := getPositionAmount(mint)
					// sellAmount := LamportToUi(getPositionAmount(mint), mint)
					log.Printf("Swapping %f %s -> %s\n", sellBalance, tokenInfo.Symbol, sellSym)

					_, err = swapJupiter(ctx, client, mint, config.ActionWallet.SellMint, sellBalanceLamp, 0, 0)
					if err != nil {
						log.Printf("Failed to sell token: %v", err)
						continue
					}
					removePosition(mint)
					if verbose {
						log.Printf("Position %s removed.\n", tokenData[mint].Symbol)
					}
				} else {
					if verbose {
						log.Printf("Skipping trade for %s\n", tokenInfo.Symbol)
					}
				}
			}

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
		solPrice, err := fetchTokenPriceJupiter(SOL) // SOL mint address
		if err != nil {
			if verbose {
				log.Printf("failed to fetch SOL price: %v\n", err)
			}
			break
		}

		// Calculate SOL USD value
		solUSDValue := solBalanceInSOL * solPrice[SOL]

		// Add SOL to the wallet map
		tokenMap[SOL] = TokenInfo{
			Address:  SOL,
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
			tokenInfo, err := GetTokenInfo(mint)
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

// getTokenBalance retrieves the amount of a token (specified by mint) held by a wallet address.
func getTokenBalance(client *rpc.Client, wallet, mint string) (float64, error) {
	// Convert wallet address to solana.PublicKey
	walletPubkey, err := solana.PublicKeyFromBase58(wallet)
	if err != nil {
		return 0, fmt.Errorf("invalid wallet address: %w", err)
	}

	// Convert mint address to solana.PublicKey
	mintPubkey, err := solana.PublicKeyFromBase58(mint)
	if err != nil {
		return 0, fmt.Errorf("invalid mint address: %w", err)
	}

	// Find the associated token account for the wallet and mint
	associatedTokenAccount, _, err := solana.FindAssociatedTokenAddress(walletPubkey, mintPubkey)
	if err != nil {
		return 0, fmt.Errorf("failed to find associated token account: %w", err)
	}

	// Get the token account balance
	balance, err := client.GetTokenAccountBalance(context.Background(), associatedTokenAccount, rpc.CommitmentConfirmed)
	if err != nil {
		return 0, fmt.Errorf("failed to get token account balance: %w", err)
	}

	return *balance.Value.UiAmount, nil
}

// includeTokenFilter checks if a token should be included based on the configuration
func includeTokenFilter(mint string) bool {
	// If include_tokens is not empty, only include tokens in the list
	if len(config.TokenFilter.IncludeTokenList) > 0 {
		for _, includedMint := range config.TokenFilter.IncludeTokenList {
			if includedMint == mint {
				return true
			}
		}
		return false
	}

	// If exclude_tokens is not empty, exclude tokens in the list
	for _, excludedMint := range config.TokenFilter.ExcludeTokenList {
		if excludedMint == mint {
			return false
		}
	}

	// Otherwise, include the token
	return true
}
