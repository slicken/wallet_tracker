package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
)

// GetQuoteParams defines the parameters for requesting a quote from Jupiter.
type GetQuoteParams struct {
	InputMint                            string    `json:"inputMint"`
	OutputMint                           string    `json:"outputMint"`
	Amount                               int       `json:"amount"`
	SlippageBps                          *int      `json:"slippageBps,omitempty"`
	AutoSlippage                         *bool     `json:"autoSlippage,omitempty"`
	AutoSlippageCollisionUsdValue        *int      `json:"autoSlippageCollisionUsdValue,omitempty"`
	ComputeAutoSlippage                  *bool     `json:"computeAutoSlippage,omitempty"`
	MaxAutoSlippageBps                   *int      `json:"maxAutoSlippageBps,omitempty"`
	SwapMode                             *string   `json:"swapMode,omitempty"`
	Dexes                                *[]string `json:"dexes,omitempty"`
	ExcludeDexes                         *[]string `json:"excludeDexes,omitempty"`
	RestrictIntermediateTokens           *bool     `json:"restrictIntermediateTokens,omitempty"`
	OnlyDirectRoutes                     *bool     `json:"onlyDirectRoutes,omitempty"`
	AsLegacyTransaction                  *bool     `json:"asLegacyTransaction,omitempty"`
	PlatformFeeBps                       *int      `json:"platformFeeBps,omitempty"`
	MaxAccounts                          *int      `json:"maxAccounts,omitempty"`
	MinimizeSlippage                     *bool     `json:"minimizeSlippage,omitempty"`
	PreferLiquidDexes                    *bool     `json:"preferLiquidDexes,omitempty"`
	TokenCategoryBasedIntermediateTokens *bool     `json:"tokenCategoryBasedIntermediateTokens,omitempty"`
}

// QuoteResult defines the response structure for a quote from Jupiter.
type QuoteResult struct {
	ComputedAutoSlippage *int            `json:"computedAutoSlippage,omitempty"`
	ContextSlot          *float32        `json:"contextSlot,omitempty"`
	InAmount             string          `json:"inAmount"`
	InputMint            string          `json:"inputMint"`
	OtherAmountThreshold string          `json:"otherAmountThreshold"`
	OutAmount            string          `json:"outAmount"`
	OutputMint           string          `json:"outputMint"`
	PlatformFee          *PlatformFee    `json:"platformFee,omitempty"`
	PriceImpactPct       string          `json:"priceImpactPct"`
	RoutePlan            []RoutePlanStep `json:"routePlan"`
	SlippageBps          int32           `json:"slippageBps"`
	SwapMode             string          `json:"swapMode"`
	TimeTaken            *float32        `json:"timeTaken,omitempty"`
}

// PlatformFee defines the platform fee structure in a quote response.
type PlatformFee struct {
	Amount *string `json:"amount,omitempty"`
	FeeBps *int32  `json:"feeBps,omitempty"`
}

// RoutePlanStep defines a step in the swap route plan.
type RoutePlanStep struct {
	Percent  int32    `json:"percent"`
	SwapInfo SwapInfo `json:"swapInfo"`
}

// SwapInfo defines the swap information for a route plan step.
type SwapInfo struct {
	AmmKey     string  `json:"ammKey"`
	FeeAmount  string  `json:"feeAmount"`
	FeeMint    string  `json:"feeMint"`
	InAmount   string  `json:"inAmount"`
	InputMint  string  `json:"inputMint"`
	Label      *string `json:"label,omitempty"`
	OutAmount  string  `json:"outAmount"`
	OutputMint string  `json:"outputMint"`
}

// SwapRequest defines the request structure for posting a swap to Jupiter.
type SwapRequest struct {
	AllowOptimizedWrappedSolTokenAccount *bool   `json:"allowOptimizedWrappedSolTokenAccount,omitempty"`
	AsLegacyTransaction                  *bool   `json:"asLegacyTransaction,omitempty"`
	BlockhashSlotsToExpiry               *int    `json:"blockhashSlotsToExpiry,omitempty"`
	ComputeUnitPriceMicroLamports        *int    `json:"computeUnitPriceMicroLamports,omitempty"`
	CorrectLastValidBlockHeight          *bool   `json:"correctLastValidBlockHeight,omitempty"`
	DestinationTokenAccount              *string `json:"destinationTokenAccount,omitempty"`
	DynamicComputeUnitLimit              *bool   `json:"dynamicComputeUnitLimit,omitempty"`
	DynamicSlippage                      *struct {
		MaxBps *int `json:"maxBps,omitempty"`
		MinBps *int `json:"minBps,omitempty"`
	} `json:"dynamicSlippage,omitempty"`
	FeeAccount                *string     `json:"feeAccount,omitempty"`
	PrioritizationFeeLamports *string     `json:"prioritizationFeeLamports,omitempty"`
	ProgramAuthorityId        *int        `json:"programAuthorityId,omitempty"`
	QuoteResponse             QuoteResult `json:"quoteResponse"`
	SkipUserAccountsRpcCalls  *bool       `json:"skipUserAccountsRpcCalls,omitempty"`
	UseSharedAccounts         *bool       `json:"useSharedAccounts,omitempty"`
	UseTokenLedger            *bool       `json:"useTokenLedger,omitempty"`
	UserPublicKey             string      `json:"userPublicKey"`
	WrapAndUnwrapSol          *bool       `json:"wrapAndUnwrapSol,omitempty"`
}

// SwapResponse defines the response structure for a swap transaction from Jupiter.
type SwapResponse struct {
	DynamicSlippageReport *struct {
		AmplificationRatio           *string `json:"amplificationRatio,omitempty"`
		CategoryName                 *string `json:"categoryName,omitempty"`
		HeuristicMaxSlippageBps      *int    `json:"heuristicMaxSlippageBps,omitempty"`
		OtherAmount                  *int    `json:"otherAmount,omitempty"`
		SimulatedIncurredSlippageBps *int    `json:"simulatedIncurredSlippageBps,omitempty"`
		SlippageBps                  *int    `json:"slippageBps,omitempty"`
	} `json:"dynamicSlippageReport,omitempty"`
	LastValidBlockHeight      float32  `json:"lastValidBlockHeight"`
	PrioritizationFeeLamports *float32 `json:"prioritizationFeeLamports,omitempty"`
	SwapTransaction           string   `json:"swapTransaction"`
}

// getQuoteJupiter requests a quote from the Jupiter API.
func getQuoteJupiter(ctx context.Context, params GetQuoteParams) (*QuoteResult, error) {
	client := &http.Client{}
	baseURL := "https://quote-api.jup.ag/v6/quote"

	// Construct query parameters using url.Values
	queryParams := url.Values{}
	queryParams.Add("inputMint", params.InputMint)
	queryParams.Add("outputMint", params.OutputMint)
	queryParams.Add("amount", strconv.Itoa(params.Amount))

	if params.SlippageBps != nil {
		queryParams.Add("slippageBps", strconv.Itoa(*params.SlippageBps))
	}
	if params.AutoSlippage != nil {
		queryParams.Add("autoSlippage", strconv.FormatBool(*params.AutoSlippage))
	}
	if params.AutoSlippageCollisionUsdValue != nil {
		queryParams.Add("autoSlippageCollisionUsdValue", strconv.Itoa(*params.AutoSlippageCollisionUsdValue))
	}
	if params.ComputeAutoSlippage != nil {
		queryParams.Add("computeAutoSlippage", strconv.FormatBool(*params.ComputeAutoSlippage))
	}
	if params.MaxAutoSlippageBps != nil {
		queryParams.Add("maxAutoSlippageBps", strconv.Itoa(*params.MaxAutoSlippageBps))
	}
	if params.SwapMode != nil {
		queryParams.Add("swapMode", *params.SwapMode)
	}
	if params.Dexes != nil {
		queryParams.Add("dexes", strings.Join(*params.Dexes, ","))
	}
	if params.ExcludeDexes != nil {
		queryParams.Add("excludeDexes", strings.Join(*params.ExcludeDexes, ","))
	}
	if params.RestrictIntermediateTokens != nil {
		queryParams.Add("restrictIntermediateTokens", strconv.FormatBool(*params.RestrictIntermediateTokens))
	}
	if params.OnlyDirectRoutes != nil {
		queryParams.Add("onlyDirectRoutes", strconv.FormatBool(*params.OnlyDirectRoutes))
	}
	if params.AsLegacyTransaction != nil {
		queryParams.Add("asLegacyTransaction", strconv.FormatBool(*params.AsLegacyTransaction))
	}
	if params.PlatformFeeBps != nil {
		queryParams.Add("platformFeeBps", strconv.Itoa(*params.PlatformFeeBps))
	}
	if params.MaxAccounts != nil {
		queryParams.Add("maxAccounts", strconv.Itoa(*params.MaxAccounts))
	}
	if params.MinimizeSlippage != nil {
		queryParams.Add("minimizeSlippage", strconv.FormatBool(*params.MinimizeSlippage))
	}
	if params.PreferLiquidDexes != nil {
		queryParams.Add("preferLiquidDexes", strconv.FormatBool(*params.PreferLiquidDexes))
	}
	if params.TokenCategoryBasedIntermediateTokens != nil {
		queryParams.Add("tokenCategoryBasedIntermediateTokens", strconv.FormatBool(*params.TokenCategoryBasedIntermediateTokens))
	}

	// Build the full URL with query parameters
	fullURL := baseURL + "?" + queryParams.Encode()

	// Create the HTTP request
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get quote: %s", resp.Status)
	}

	// Decode the response into a QuoteResult
	var result QuoteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// postSwapJupiter posts a swap request to the Jupiter API.
func postSwapJupiter(ctx context.Context, request SwapRequest) (*SwapResponse, error) {
	client := &http.Client{}
	url := "https://quote-api.jup.ag/v6/swap"

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to post swap: %s, response: %s", resp.Status, string(body))
	}

	// Decode the response into a SwapResponse
	var result SwapResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// swapJupiter performs a swap on Jupiter, including retry logic for both quote and swap requests.
func swapJupiter(ctx context.Context, client *rpc.Client, inputMint, outputMint string, amountLamport int64, slippage int, priorityFee int) (int64, error) {
	// Set default slippage if not provided
	if slippage == 0 {
		slippage = 50
	}

	if verbose {
		log.Println("Requesting quote from Jupiter...")
	}

	// Retry logic for getting a quote
	var quoteResponse *QuoteResult
	err := retryRPC(func() error {
		resp, err := getQuoteJupiter(ctx, GetQuoteParams{
			InputMint:   inputMint,
			OutputMint:  outputMint,
			Amount:      int(amountLamport),
			SlippageBps: &slippage,
		})
		if err != nil {
			return err
		}
		if resp == nil {
			return fmt.Errorf("invalid response from Jupiter API")
		}
		quoteResponse = resp
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get quote: %w", err)
	}

	dynamicComputeUnitLimit := true

	if verbose {
		log.Println("Sending swap request to Jupiter...")
	}

	// Set prioritization fee to "auto" if priorityFee is 0, otherwise use the provided value
	var prioritizationFeeLamports *string
	if priorityFee == 0 {
		auto := "auto"
		prioritizationFeeLamports = &auto
	} else {
		fee := strconv.Itoa(priorityFee)
		prioritizationFeeLamports = &fee
	}

	// Prepare the swap request
	swapRequest := SwapRequest{
		QuoteResponse:             *quoteResponse,
		UserPublicKey:             config.ActionWallet.PublicKey,
		DynamicComputeUnitLimit:   &dynamicComputeUnitLimit,
		PrioritizationFeeLamports: prioritizationFeeLamports,
	}

	// Retry logic for posting a swap
	var swapResponse *SwapResponse
	err = retryRPC(func() error {
		resp, err := postSwapJupiter(ctx, swapRequest)
		if err != nil {
			return err
		}
		if resp == nil {
			return fmt.Errorf("invalid response from Jupiter API")
		}
		swapResponse = resp
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to post swap: %w", err)
	}

	// Decode the base64-encoded transaction from the swap response
	txData, err := base64.StdEncoding.DecodeString(swapResponse.SwapTransaction)
	if err != nil {
		return 0, fmt.Errorf("failed to decode base64 transaction: %w", err)
	}

	// Deserialize the transaction using the solana-go library
	var tx solana.Transaction
	if err := tx.UnmarshalWithDecoder(bin.NewBinDecoder(txData)); err != nil {
		return 0, fmt.Errorf("failed to deserialize transaction: %w", err)
	}

	// Refresh the blockhash before signing
	var recentBlockhash *rpc.GetLatestBlockhashResult
	err = retryRPC(func() error {
		bh, err := client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
		if err != nil {
			return err
		}
		recentBlockhash = bh
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to fetch latest blockhash: %w", err)
	}
	tx.Message.RecentBlockhash = recentBlockhash.Value.Blockhash

	// Create a wallet from private key
	signer := solana.MustPrivateKeyFromBase58(config.ActionWallet.PrivateKey)

	// Sign the transaction with the wallet's private key
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if signer.PublicKey().Equals(key) {
			return &signer
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Retry logic for sending the transaction
	var txSig solana.Signature
	err = retryRPC(func() error {
		sig, err := client.SendTransaction(ctx, &tx)
		if err != nil {
			return err
		}
		txSig = sig
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to send transaction: %w", err)
	}

	if verbose {
		log.Println("Swap signed! Waiting for confirmation...")
	}

	// Wait for confirmation of the transaction
	if err := confirmTransaction(ctx, txSig); err != nil {
		return 0, fmt.Errorf("failed to confirm transaction: %w", err)
	}

	log.Printf("Swap confirmed! %s\n", txSig)

	// Parse the output amount from the quote response
	outAmount, err := strconv.ParseInt(quoteResponse.OutAmount, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse outAmount: %w", err)
	}

	// Return the output amount
	return outAmount, nil
}

// confirmTransaction waits for the transaction to be confirmed using a
// confirmTransaction sets up a WebSocket connection and subscribes to the transaction signature.
// It waits until the transaction is finalized.
func confirmTransaction(ctx context.Context, txSig solana.Signature) error {
	// Create a new WebSocket client.
	wsClient, err := ws.Connect(ctx, rpc.MainNetBeta_WS)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	// Subscribe to the transaction signature.
	sub, err := wsClient.SignatureSubscribe(txSig, rpc.CommitmentConfirmed)
	if err != nil {
		return fmt.Errorf("failed to subscribe to signature: %w", err)
	}
	defer sub.Unsubscribe()

	// Wait for the transaction to be finalized.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case resp, ok := <-sub.Response():
			if !ok {
				return fmt.Errorf("WebSocket subscription closed unexpectedly")
			}
			if resp.Value.Err != nil {
				return fmt.Errorf("transaction failed: %v", resp.Value.Err)
			}
			// Transaction is confirmed.
			return nil
		}
	}
}
