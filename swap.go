package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"

	"github.com/oapi-codegen/runtime"
)

func swapJupiter(ctx context.Context, client *rpc.Client, inputMint, outputMint string, amount float64, slippage int, priorityFee float64) (float64, error) {
	// Get token metadata
	var decimals float64
	if inputMint == SOL {
		decimals = 9
	} else {
		inputTokendata, exist := tokenData[inputMint]
		if !exist {
			// Fallback to standard decimal value (6 for most tokens)
			decimals = 6
			log.Printf("No metadata found for inputMint: %s. Using standard decimals: %f\n", inputMint, decimals)
		} else {
			decimals = float64(inputTokendata.Decimals)
		}
	}

	// Get the output token decimals
	var outTokenDecimals float64
	if outputMint == SOL {
		// SOL has 9 decimals
		outTokenDecimals = 9
	} else {
		outputTokendata, exist := tokenData[outputMint]
		if !exist {
			// Fallback to standard decimal value (6 for most tokens)
			outTokenDecimals = 6
			log.Printf("No metadata found for outputMint: %s. Using standard decimals: %f\n", outputMint, outTokenDecimals)
		} else {
			outTokenDecimals = float64(outputTokendata.Decimals)
		}
	}

	// Calculate amountLamport as an integer
	amountLamport := int(math.Round(amount * math.Pow(10, decimals)))

	// Ensure amountLamport is a positive integer
	if amountLamport <= 0 {
		return 0, fmt.Errorf("invalid amount: must be greater than 0")
	}

	// Convert amountLamport to a string (if the API expects a string)
	if slippage == 0 {
		slippage = 250 // Default slippage of 2.5%
	}

	DefaultAPIURL := "https://quote-api.jup.ag/v6"
	jupClient, err := NewClientWithResponses(DefaultAPIURL)
	if err != nil {
		return 0, fmt.Errorf("failed to create Jupiter client: %w", err)
	}

	if verbose {
		log.Println("Requesting quote from Jupiter...")
	}

	var quoteResponse GetQuoteResponse
	err = retryRPC(func() error {
		resp, err := jupClient.GetQuoteWithResponse(ctx, &GetQuoteParams{
			InputMint:   inputMint,
			OutputMint:  outputMint,
			Amount:      amountLamport,
			SlippageBps: &slippage,
		})
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("invalid response from Jupiter API: %s", string(resp.Body))
		}
		quoteResponse = *resp
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get quote: %w", err)
	}

	// Extract the output amount from the quote response
	outAmountStr := quoteResponse.JSON200.OutAmount // This is a string

	// Parse the string into an integer
	outAmount, err := strconv.ParseInt(outAmountStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse outAmount: %w", err)
	}

	// Format the output amount using the token's decimals
	outAmountFormatted := float64(outAmount) / math.Pow(10, outTokenDecimals)

	prioritizationFeeLamports := SwapRequest_PrioritizationFeeLamports{}
	if err = prioritizationFeeLamports.UnmarshalJSON([]byte(`"auto"`)); err != nil {
		return 0, fmt.Errorf("failed to unmarshal prioritization fee: %w", err)
	}

	dynamicComputeUnitLimit := true

	if verbose {
		log.Println("Sending swap request to Jupiter...")
	}

	var swapResponse *PostSwapResponse
	err = retryRPC(func() error {
		resp, err := jupClient.PostSwapWithResponse(ctx, PostSwapJSONRequestBody{
			PrioritizationFeeLamports: &prioritizationFeeLamports,
			QuoteResponse:             *quoteResponse.JSON200,
			UserPublicKey:             config.ActionWallet.PublicKey,
			DynamicComputeUnitLimit:   &dynamicComputeUnitLimit,
		})
		if err != nil {
			return err
		}
		swapResponse = resp
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to post swap: %w", err)
	}

	// Decode the base64-encoded transaction from the swap response
	txData, err := base64.StdEncoding.DecodeString(swapResponse.JSON200.SwapTransaction)
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

	log.Printf("Swap signed! %s\n", txSig)

	if verbose {
		log.Println("Waiting for confirmation...")
	}

	// Wait for confirmation of the transaction
	if err := confirmTransaction(ctx, txSig); err != nil {
		return 0, fmt.Errorf("failed to confirm transaction: %w", err)
	}

	log.Printf("Swap confirmed!")

	// Return the output amount
	return outAmountFormatted, nil
}

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

const (
	SwapModeExactIn  SwapMode = "ExactIn"
	SwapModeExactOut SwapMode = "ExactOut"
)

const (
	SwapRequestComputeUnitPriceMicroLamports1Auto SwapRequestComputeUnitPriceMicroLamports1 = "auto"
)

const (
	SwapRequestPrioritizationFeeLamports1Auto SwapRequestPrioritizationFeeLamports1 = "auto"
)

const (
	Bluechip SwapResponseDynamicSlippageReportCategoryName = "bluechip"
	Lst      SwapResponseDynamicSlippageReportCategoryName = "lst"
	Stable   SwapResponseDynamicSlippageReportCategoryName = "stable"
	Verified SwapResponseDynamicSlippageReportCategoryName = "verified"
)

// Defines values for SwapModeParameter.
const (
	SwapModeParameterExactIn  SwapModeParameter = "ExactIn"
	SwapModeParameterExactOut SwapModeParameter = "ExactOut"
)

// Defines values for GetQuoteParamsSwapMode.
const (
	ExactIn  GetQuoteParamsSwapMode = "ExactIn"
	ExactOut GetQuoteParamsSwapMode = "ExactOut"
)

// AccountMeta defines model for AccountMeta.
type AccountMeta struct {
	IsSigner   bool   `json:"isSigner"`
	IsWritable bool   `json:"isWritable"`
	Pubkey     string `json:"pubkey"`
}

// IndexedRouteMapResponse defines model for IndexedRouteMapResponse.
type IndexedRouteMapResponse struct {
	IndexedRouteMap map[string][]float32 `json:"indexedRouteMap"`
	MintKeys        []string             `json:"mintKeys"`
}

// Instruction defines model for Instruction.
type Instruction struct {
	Accounts  []AccountMeta `json:"accounts"`
	Data      string        `json:"data"`
	ProgramId string        `json:"programId"`
}

// PlatformFee defines model for PlatformFee.
type PlatformFee struct {
	Amount *string `json:"amount,omitempty"`
	FeeBps *int32  `json:"feeBps,omitempty"`
}

// QuoteResponse defines model for QuoteResponse.
type QuoteResponse struct {
	ComputedAutoSlippage *int32          `json:"computedAutoSlippage,omitempty"`
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
	SwapMode             SwapMode        `json:"swapMode"`
	TimeTaken            *float32        `json:"timeTaken,omitempty"`
}

// RoutePlanStep defines model for RoutePlanStep.
type RoutePlanStep struct {
	Percent  int32    `json:"percent"`
	SwapInfo SwapInfo `json:"swapInfo"`
}

// SwapInfo defines model for SwapInfo.
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

// SwapInstructionsResponse defines model for SwapInstructionsResponse.
type SwapInstructionsResponse struct {
	AddressLookupTableAddresses []string      `json:"addressLookupTableAddresses"`
	CleanupInstruction          *Instruction  `json:"cleanupInstruction,omitempty"`
	ComputeBudgetInstructions   []Instruction `json:"computeBudgetInstructions"`
	SetupInstructions           []Instruction `json:"setupInstructions"`
	SwapInstruction             Instruction   `json:"swapInstruction"`
	TokenLedgerInstruction      *Instruction  `json:"tokenLedgerInstruction,omitempty"`
}

// SwapMode defines model for SwapMode.
type SwapMode string

// SwapRequest defines model for SwapRequest.
type SwapRequest struct {
	AllowOptimizedWrappedSolTokenAccount *bool                                      `json:"allowOptimizedWrappedSolTokenAccount,omitempty"`
	AsLegacyTransaction                  *bool                                      `json:"asLegacyTransaction,omitempty"`
	BlockhashSlotsToExpiry               *float32                                   `json:"blockhashSlotsToExpiry,omitempty"`
	ComputeUnitPriceMicroLamports        *SwapRequest_ComputeUnitPriceMicroLamports `json:"computeUnitPriceMicroLamports,omitempty"`
	CorrectLastValidBlockHeight          *bool                                      `json:"correctLastValidBlockHeight,omitempty"`
	DestinationTokenAccount              *string                                    `json:"destinationTokenAccount,omitempty"`
	DynamicComputeUnitLimit              *bool                                      `json:"dynamicComputeUnitLimit,omitempty"`
	DynamicSlippage                      *struct {
		MaxBps *int `json:"maxBps,omitempty"`
		MinBps *int `json:"minBps,omitempty"`
	} `json:"dynamicSlippage,omitempty"`
	FeeAccount                *string                                `json:"feeAccount,omitempty"`
	PrioritizationFeeLamports *SwapRequest_PrioritizationFeeLamports `json:"prioritizationFeeLamports,omitempty"`
	ProgramAuthorityId        *int                                   `json:"programAuthorityId,omitempty"`
	QuoteResponse             QuoteResponse                          `json:"quoteResponse"`
	SkipUserAccountsRpcCalls  *bool                                  `json:"skipUserAccountsRpcCalls,omitempty"`
	UseSharedAccounts         *bool                                  `json:"useSharedAccounts,omitempty"`
	UseTokenLedger            *bool                                  `json:"useTokenLedger,omitempty"`
	UserPublicKey             string                                 `json:"userPublicKey"`
	WrapAndUnwrapSol          *bool                                  `json:"wrapAndUnwrapSol,omitempty"`
}

// SwapRequestComputeUnitPriceMicroLamports0 defines model for .
type SwapRequestComputeUnitPriceMicroLamports0 = int

// SwapRequestComputeUnitPriceMicroLamports1 defines model for SwapRequest.ComputeUnitPriceMicroLamports.1.
type SwapRequestComputeUnitPriceMicroLamports1 string

// SwapRequest_ComputeUnitPriceMicroLamports The compute unit price to prioritize the transaction, the additional fee will be `computeUnitLimit (1400000) * computeUnitPriceMicroLamports`. If `auto` is used, Jupiter will automatically set a priority fee and it will be capped at 5,000,000 lamports / 0.005 SOL.
type SwapRequest_ComputeUnitPriceMicroLamports struct {
	union json.RawMessage
}

// SwapRequestPrioritizationFeeLamports0 defines model for .
type SwapRequestPrioritizationFeeLamports0 = int

// SwapRequestPrioritizationFeeLamports1 defines model for SwapRequest.PrioritizationFeeLamports.1.
type SwapRequestPrioritizationFeeLamports1 string

// SwapRequest_PrioritizationFeeLamports \* PriorityFeeWithMaxLamports is impossible to be typed. Prioritization fee lamports paid for the transaction in addition to the signatures fee. Mutually exclusive with compute_unit_price_micro_lamports. If `auto` is used, Jupiter will automatically set a priority fee and it will be capped at 5,000,000 lamports / 0.005 SOL.
type SwapRequest_PrioritizationFeeLamports struct {
	union json.RawMessage
}

// SwapResponse defines model for SwapResponse.
type SwapResponse struct {
	DynamicSlippageReport *struct {
		AmplificationRatio           *string                                        `json:"amplificationRatio,omitempty"`
		CategoryName                 *SwapResponseDynamicSlippageReportCategoryName `json:"categoryName,omitempty"`
		HeuristicMaxSlippageBps      *int                                           `json:"heuristicMaxSlippageBps,omitempty"`
		OtherAmount                  *int                                           `json:"otherAmount,omitempty"`
		SimulatedIncurredSlippageBps *int                                           `json:"simulatedIncurredSlippageBps,omitempty"`
		SlippageBps                  *int                                           `json:"slippageBps,omitempty"`
	} `json:"dynamicSlippageReport,omitempty"`
	LastValidBlockHeight      float32  `json:"lastValidBlockHeight"`
	PrioritizationFeeLamports *float32 `json:"prioritizationFeeLamports,omitempty"`
	SwapTransaction           string   `json:"swapTransaction"`
}

// SwapResponseDynamicSlippageReportCategoryName defines model for SwapResponse.DynamicSlippageReport.CategoryName.
type SwapResponseDynamicSlippageReportCategoryName string
type AmountParameter = int
type AsLegacyTransactionParameter = bool
type AutoSlippageCollisionValueParameter = int
type AutoSlippageParameter = bool
type ComputeAutoSlippageParameter = bool
type DexesParameter = []string
type ExcludeDexesParameter = []string
type InputMintParameter = string
type MaxAccountsParameter = int
type MaxAutoSlippageBpsParameter = int
type MinimizeSlippage = bool
type OnlyDirectRoutesParameter = bool
type OutputMintParameter = string
type PlatformFeeBpsParameter = int
type PreferLiquidDexes = bool
type RestrictIntermediateTokensParameter = bool
type SlippageParameter = int
type SwapModeParameter string
type TokenCategoryBasedIntermediateTokensParameter = bool
type GetIndexedRouteMapParams struct {
	OnlyDirectRoutes *OnlyDirectRoutesParameter `form:"onlyDirectRoutes,omitempty" json:"onlyDirectRoutes,omitempty"`
}

// GetQuoteParams defines parameters for GetQuote.
type GetQuoteParams struct {
	InputMint                            InputMintParameter                             `form:"inputMint" json:"inputMint"`
	OutputMint                           OutputMintParameter                            `form:"outputMint" json:"outputMint"`
	Amount                               AmountParameter                                `form:"amount" json:"amount"`
	SlippageBps                          *SlippageParameter                             `form:"slippageBps,omitempty" json:"slippageBps,omitempty"`
	AutoSlippage                         *AutoSlippageParameter                         `form:"autoSlippage,omitempty" json:"autoSlippage,omitempty"`
	AutoSlippageCollisionUsdValue        *AutoSlippageCollisionValueParameter           `form:"autoSlippageCollisionUsdValue,omitempty" json:"autoSlippageCollisionUsdValue,omitempty"`
	ComputeAutoSlippage                  *ComputeAutoSlippageParameter                  `form:"computeAutoSlippage,omitempty" json:"computeAutoSlippage,omitempty"`
	MaxAutoSlippageBps                   *MaxAutoSlippageBpsParameter                   `form:"maxAutoSlippageBps,omitempty" json:"maxAutoSlippageBps,omitempty"`
	SwapMode                             *GetQuoteParamsSwapMode                        `form:"swapMode,omitempty" json:"swapMode,omitempty"`
	Dexes                                *DexesParameter                                `form:"dexes,omitempty" json:"dexes,omitempty"`
	ExcludeDexes                         *ExcludeDexesParameter                         `form:"excludeDexes,omitempty" json:"excludeDexes,omitempty"`
	RestrictIntermediateTokens           *RestrictIntermediateTokensParameter           `form:"restrictIntermediateTokens,omitempty" json:"restrictIntermediateTokens,omitempty"`
	OnlyDirectRoutes                     *OnlyDirectRoutesParameter                     `form:"onlyDirectRoutes,omitempty" json:"onlyDirectRoutes,omitempty"`
	AsLegacyTransaction                  *AsLegacyTransactionParameter                  `form:"asLegacyTransaction,omitempty" json:"asLegacyTransaction,omitempty"`
	PlatformFeeBps                       *PlatformFeeBpsParameter                       `form:"platformFeeBps,omitempty" json:"platformFeeBps,omitempty"`
	MaxAccounts                          *MaxAccountsParameter                          `form:"maxAccounts,omitempty" json:"maxAccounts,omitempty"`
	MinimizeSlippage                     *MinimizeSlippage                              `form:"minimizeSlippage,omitempty" json:"minimizeSlippage,omitempty"`
	PreferLiquidDexes                    *PreferLiquidDexes                             `form:"preferLiquidDexes,omitempty" json:"preferLiquidDexes,omitempty"`
	TokenCategoryBasedIntermediateTokens *TokenCategoryBasedIntermediateTokensParameter `form:"tokenCategoryBasedIntermediateTokens,omitempty" json:"tokenCategoryBasedIntermediateTokens,omitempty"`
}

type GetQuoteParamsSwapMode string
type PostSwapJSONRequestBody = SwapRequest
type PostSwapInstructionsJSONRequestBody = SwapRequest

// AsSwapRequestComputeUnitPriceMicroLamports0 returns the union data inside the SwapRequest_ComputeUnitPriceMicroLamports as a SwapRequestComputeUnitPriceMicroLamports0
func (t SwapRequest_ComputeUnitPriceMicroLamports) AsSwapRequestComputeUnitPriceMicroLamports0() (SwapRequestComputeUnitPriceMicroLamports0, error) {
	var body SwapRequestComputeUnitPriceMicroLamports0
	err := json.Unmarshal(t.union, &body)
	return body, err
}

// FromSwapRequestComputeUnitPriceMicroLamports0 overwrites any union data inside the SwapRequest_ComputeUnitPriceMicroLamports as the provided SwapRequestComputeUnitPriceMicroLamports0
func (t *SwapRequest_ComputeUnitPriceMicroLamports) FromSwapRequestComputeUnitPriceMicroLamports0(v SwapRequestComputeUnitPriceMicroLamports0) error {
	b, err := json.Marshal(v)
	t.union = b
	return err
}

// MergeSwapRequestComputeUnitPriceMicroLamports0 performs a merge with any union data inside the SwapRequest_ComputeUnitPriceMicroLamports, using the provided SwapRequestComputeUnitPriceMicroLamports0
func (t *SwapRequest_ComputeUnitPriceMicroLamports) MergeSwapRequestComputeUnitPriceMicroLamports0(v SwapRequestComputeUnitPriceMicroLamports0) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	merged, err := runtime.JSONMerge(t.union, b)
	t.union = merged
	return err
}

// AsSwapRequestComputeUnitPriceMicroLamports1 returns the union data inside the SwapRequest_ComputeUnitPriceMicroLamports as a SwapRequestComputeUnitPriceMicroLamports1
func (t SwapRequest_ComputeUnitPriceMicroLamports) AsSwapRequestComputeUnitPriceMicroLamports1() (SwapRequestComputeUnitPriceMicroLamports1, error) {
	var body SwapRequestComputeUnitPriceMicroLamports1
	err := json.Unmarshal(t.union, &body)
	return body, err
}

// FromSwapRequestComputeUnitPriceMicroLamports1 overwrites any union data inside the SwapRequest_ComputeUnitPriceMicroLamports as the provided SwapRequestComputeUnitPriceMicroLamports1
func (t *SwapRequest_ComputeUnitPriceMicroLamports) FromSwapRequestComputeUnitPriceMicroLamports1(v SwapRequestComputeUnitPriceMicroLamports1) error {
	b, err := json.Marshal(v)
	t.union = b
	return err
}

// MergeSwapRequestComputeUnitPriceMicroLamports1 performs a merge with any union data inside the SwapRequest_ComputeUnitPriceMicroLamports, using the provided SwapRequestComputeUnitPriceMicroLamports1
func (t *SwapRequest_ComputeUnitPriceMicroLamports) MergeSwapRequestComputeUnitPriceMicroLamports1(v SwapRequestComputeUnitPriceMicroLamports1) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	merged, err := runtime.JSONMerge(t.union, b)
	t.union = merged
	return err
}

func (t SwapRequest_ComputeUnitPriceMicroLamports) MarshalJSON() ([]byte, error) {
	b, err := t.union.MarshalJSON()
	return b, err
}

func (t *SwapRequest_ComputeUnitPriceMicroLamports) UnmarshalJSON(b []byte) error {
	err := t.union.UnmarshalJSON(b)
	return err
}

// AsSwapRequestPrioritizationFeeLamports0 returns the union data inside the SwapRequest_PrioritizationFeeLamports as a SwapRequestPrioritizationFeeLamports0
func (t SwapRequest_PrioritizationFeeLamports) AsSwapRequestPrioritizationFeeLamports0() (SwapRequestPrioritizationFeeLamports0, error) {
	var body SwapRequestPrioritizationFeeLamports0
	err := json.Unmarshal(t.union, &body)
	return body, err
}

// FromSwapRequestPrioritizationFeeLamports0 overwrites any union data inside the SwapRequest_PrioritizationFeeLamports as the provided SwapRequestPrioritizationFeeLamports0
func (t *SwapRequest_PrioritizationFeeLamports) FromSwapRequestPrioritizationFeeLamports0(v SwapRequestPrioritizationFeeLamports0) error {
	b, err := json.Marshal(v)
	t.union = b
	return err
}

// MergeSwapRequestPrioritizationFeeLamports0 performs a merge with any union data inside the SwapRequest_PrioritizationFeeLamports, using the provided SwapRequestPrioritizationFeeLamports0
func (t *SwapRequest_PrioritizationFeeLamports) MergeSwapRequestPrioritizationFeeLamports0(v SwapRequestPrioritizationFeeLamports0) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	merged, err := runtime.JSONMerge(t.union, b)
	t.union = merged
	return err
}

// AsSwapRequestPrioritizationFeeLamports1 returns the union data inside the SwapRequest_PrioritizationFeeLamports as a SwapRequestPrioritizationFeeLamports1
func (t SwapRequest_PrioritizationFeeLamports) AsSwapRequestPrioritizationFeeLamports1() (SwapRequestPrioritizationFeeLamports1, error) {
	var body SwapRequestPrioritizationFeeLamports1
	err := json.Unmarshal(t.union, &body)
	return body, err
}

// FromSwapRequestPrioritizationFeeLamports1 overwrites any union data inside the SwapRequest_PrioritizationFeeLamports as the provided SwapRequestPrioritizationFeeLamports1
func (t *SwapRequest_PrioritizationFeeLamports) FromSwapRequestPrioritizationFeeLamports1(v SwapRequestPrioritizationFeeLamports1) error {
	b, err := json.Marshal(v)
	t.union = b
	return err
}

// MergeSwapRequestPrioritizationFeeLamports1 performs a merge with any union data inside the SwapRequest_PrioritizationFeeLamports, using the provided SwapRequestPrioritizationFeeLamports1
func (t *SwapRequest_PrioritizationFeeLamports) MergeSwapRequestPrioritizationFeeLamports1(v SwapRequestPrioritizationFeeLamports1) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	merged, err := runtime.JSONMerge(t.union, b)
	t.union = merged
	return err
}

func (t SwapRequest_PrioritizationFeeLamports) MarshalJSON() ([]byte, error) {
	b, err := t.union.MarshalJSON()
	return b, err
}

func (t *SwapRequest_PrioritizationFeeLamports) UnmarshalJSON(b []byte) error {
	err := t.union.UnmarshalJSON(b)
	return err
}

// RequestEditorFn  is the function signature for the RequestEditor callback function
type RequestEditorFn func(ctx context.Context, req *http.Request) error

type HttpRequestDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client which conforms to the OpenAPI3 specification for this service.
type Client struct {
	Server         string
	Client         HttpRequestDoer
	RequestEditors []RequestEditorFn
}

// ClientOption allows setting custom parameters during construction
type ClientOption func(*Client) error

// Creates a new Client, with reasonable defaults
func NewClient(server string, opts ...ClientOption) (*Client, error) {
	// create a client with sane default values
	client := Client{
		Server: server,
	}
	// mutate client and add all optional params
	for _, o := range opts {
		if err := o(&client); err != nil {
			return nil, err
		}
	}
	// ensure the server URL always has a trailing slash
	if !strings.HasSuffix(client.Server, "/") {
		client.Server += "/"
	}
	// create httpClient, if not already present
	if client.Client == nil {
		client.Client = &http.Client{}
	}
	return &client, nil
}

// WithHTTPClient allows overriding the default Doer, which is
// automatically created using http.Client. This is useful for tests.
func WithHTTPClient(doer HttpRequestDoer) ClientOption {
	return func(c *Client) error {
		c.Client = doer
		return nil
	}
}

// WithRequestEditorFn allows setting up a callback function, which will be
// called right before sending the request. This can be used to mutate the request.
func WithRequestEditorFn(fn RequestEditorFn) ClientOption {
	return func(c *Client) error {
		c.RequestEditors = append(c.RequestEditors, fn)
		return nil
	}
}

// The interface specification for the client above.
type ClientInterface interface {
	GetIndexedRouteMap(ctx context.Context, params *GetIndexedRouteMapParams, reqEditors ...RequestEditorFn) (*http.Response, error)
	GetProgramIdToLabel(ctx context.Context, reqEditors ...RequestEditorFn) (*http.Response, error)
	GetQuote(ctx context.Context, params *GetQuoteParams, reqEditors ...RequestEditorFn) (*http.Response, error)
	PostSwapWithBody(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*http.Response, error)
	PostSwap(ctx context.Context, body PostSwapJSONRequestBody, reqEditors ...RequestEditorFn) (*http.Response, error)
	PostSwapInstructionsWithBody(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*http.Response, error)
	PostSwapInstructions(ctx context.Context, body PostSwapInstructionsJSONRequestBody, reqEditors ...RequestEditorFn) (*http.Response, error)
	GetTokens(ctx context.Context, reqEditors ...RequestEditorFn) (*http.Response, error)
}

func (c *Client) GetIndexedRouteMap(ctx context.Context, params *GetIndexedRouteMapParams, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewGetIndexedRouteMapRequest(c.Server, params)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) GetProgramIdToLabel(ctx context.Context, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewGetProgramIdToLabelRequest(c.Server)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) GetQuote(ctx context.Context, params *GetQuoteParams, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewGetQuoteRequest(c.Server, params)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) PostSwapWithBody(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewPostSwapRequestWithBody(c.Server, contentType, body)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) PostSwap(ctx context.Context, body PostSwapJSONRequestBody, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewPostSwapRequest(c.Server, body)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) PostSwapInstructionsWithBody(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewPostSwapInstructionsRequestWithBody(c.Server, contentType, body)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) PostSwapInstructions(ctx context.Context, body PostSwapInstructionsJSONRequestBody, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewPostSwapInstructionsRequest(c.Server, body)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

func (c *Client) GetTokens(ctx context.Context, reqEditors ...RequestEditorFn) (*http.Response, error) {
	req, err := NewGetTokensRequest(c.Server)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if err := c.applyEditors(ctx, req, reqEditors); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

// NewGetIndexedRouteMapRequest generates requests for GetIndexedRouteMap
func NewGetIndexedRouteMapRequest(server string, params *GetIndexedRouteMapParams) (*http.Request, error) {
	var err error

	serverURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}

	operationPath := fmt.Sprintf("/indexed-route-map")
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}

	queryURL, err := serverURL.Parse(operationPath)
	if err != nil {
		return nil, err
	}

	if params != nil {
		queryValues := queryURL.Query()

		if params.OnlyDirectRoutes != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "onlyDirectRoutes", runtime.ParamLocationQuery, *params.OnlyDirectRoutes); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		queryURL.RawQuery = queryValues.Encode()
	}

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return nil, err
	}

	return req, nil
}

// NewGetProgramIdToLabelRequest generates requests for GetProgramIdToLabel
func NewGetProgramIdToLabelRequest(server string) (*http.Request, error) {
	var err error

	serverURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}

	operationPath := fmt.Sprintf("/program-id-to-label")
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}

	queryURL, err := serverURL.Parse(operationPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return nil, err
	}

	return req, nil
}

// NewGetQuoteRequest generates requests for GetQuote
func NewGetQuoteRequest(server string, params *GetQuoteParams) (*http.Request, error) {
	var err error

	serverURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}

	operationPath := fmt.Sprintf("/quote")
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}

	queryURL, err := serverURL.Parse(operationPath)
	if err != nil {
		return nil, err
	}

	if params != nil {
		queryValues := queryURL.Query()

		if queryFrag, err := runtime.StyleParamWithLocation("form", true, "inputMint", runtime.ParamLocationQuery, params.InputMint); err != nil {
			return nil, err
		} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
			return nil, err
		} else {
			for k, v := range parsed {
				for _, v2 := range v {
					queryValues.Add(k, v2)
				}
			}
		}

		if queryFrag, err := runtime.StyleParamWithLocation("form", true, "outputMint", runtime.ParamLocationQuery, params.OutputMint); err != nil {
			return nil, err
		} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
			return nil, err
		} else {
			for k, v := range parsed {
				for _, v2 := range v {
					queryValues.Add(k, v2)
				}
			}
		}

		if queryFrag, err := runtime.StyleParamWithLocation("form", true, "amount", runtime.ParamLocationQuery, params.Amount); err != nil {
			return nil, err
		} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
			return nil, err
		} else {
			for k, v := range parsed {
				for _, v2 := range v {
					queryValues.Add(k, v2)
				}
			}
		}

		if params.SlippageBps != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "slippageBps", runtime.ParamLocationQuery, *params.SlippageBps); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.AutoSlippage != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "autoSlippage", runtime.ParamLocationQuery, *params.AutoSlippage); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.AutoSlippageCollisionUsdValue != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "autoSlippageCollisionUsdValue", runtime.ParamLocationQuery, *params.AutoSlippageCollisionUsdValue); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.ComputeAutoSlippage != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "computeAutoSlippage", runtime.ParamLocationQuery, *params.ComputeAutoSlippage); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.MaxAutoSlippageBps != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "maxAutoSlippageBps", runtime.ParamLocationQuery, *params.MaxAutoSlippageBps); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.SwapMode != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "swapMode", runtime.ParamLocationQuery, *params.SwapMode); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.Dexes != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "dexes", runtime.ParamLocationQuery, *params.Dexes); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.ExcludeDexes != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "excludeDexes", runtime.ParamLocationQuery, *params.ExcludeDexes); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.RestrictIntermediateTokens != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "restrictIntermediateTokens", runtime.ParamLocationQuery, *params.RestrictIntermediateTokens); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.OnlyDirectRoutes != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "onlyDirectRoutes", runtime.ParamLocationQuery, *params.OnlyDirectRoutes); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.AsLegacyTransaction != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "asLegacyTransaction", runtime.ParamLocationQuery, *params.AsLegacyTransaction); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.PlatformFeeBps != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "platformFeeBps", runtime.ParamLocationQuery, *params.PlatformFeeBps); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.MaxAccounts != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "maxAccounts", runtime.ParamLocationQuery, *params.MaxAccounts); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.MinimizeSlippage != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "minimizeSlippage", runtime.ParamLocationQuery, *params.MinimizeSlippage); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.PreferLiquidDexes != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "preferLiquidDexes", runtime.ParamLocationQuery, *params.PreferLiquidDexes); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		if params.TokenCategoryBasedIntermediateTokens != nil {

			if queryFrag, err := runtime.StyleParamWithLocation("form", true, "tokenCategoryBasedIntermediateTokens", runtime.ParamLocationQuery, *params.TokenCategoryBasedIntermediateTokens); err != nil {
				return nil, err
			} else if parsed, err := url.ParseQuery(queryFrag); err != nil {
				return nil, err
			} else {
				for k, v := range parsed {
					for _, v2 := range v {
						queryValues.Add(k, v2)
					}
				}
			}

		}

		queryURL.RawQuery = queryValues.Encode()
	}

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return nil, err
	}

	return req, nil
}

// NewPostSwapRequest calls the generic PostSwap builder with application/json body
func NewPostSwapRequest(server string, body PostSwapJSONRequestBody) (*http.Request, error) {
	var bodyReader io.Reader
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	bodyReader = bytes.NewReader(buf)
	return NewPostSwapRequestWithBody(server, "application/json", bodyReader)
}

// NewPostSwapRequestWithBody generates requests for PostSwap with any type of body
func NewPostSwapRequestWithBody(server string, contentType string, body io.Reader) (*http.Request, error) {
	var err error

	serverURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}

	operationPath := fmt.Sprintf("/swap")
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}

	queryURL, err := serverURL.Parse(operationPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", queryURL.String(), body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", contentType)

	return req, nil
}

// NewPostSwapInstructionsRequest calls the generic PostSwapInstructions builder with application/json body
func NewPostSwapInstructionsRequest(server string, body PostSwapInstructionsJSONRequestBody) (*http.Request, error) {
	var bodyReader io.Reader
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	bodyReader = bytes.NewReader(buf)
	return NewPostSwapInstructionsRequestWithBody(server, "application/json", bodyReader)
}

// NewPostSwapInstructionsRequestWithBody generates requests for PostSwapInstructions with any type of body
func NewPostSwapInstructionsRequestWithBody(server string, contentType string, body io.Reader) (*http.Request, error) {
	var err error

	serverURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}

	operationPath := fmt.Sprintf("/swap-instructions")
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}

	queryURL, err := serverURL.Parse(operationPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", queryURL.String(), body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", contentType)

	return req, nil
}

// NewGetTokensRequest generates requests for GetTokens
func NewGetTokensRequest(server string) (*http.Request, error) {
	var err error

	serverURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}

	operationPath := fmt.Sprintf("/tokens")
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}

	queryURL, err := serverURL.Parse(operationPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return nil, err
	}

	return req, nil
}

func (c *Client) applyEditors(ctx context.Context, req *http.Request, additionalEditors []RequestEditorFn) error {
	for _, r := range c.RequestEditors {
		if err := r(ctx, req); err != nil {
			return err
		}
	}
	for _, r := range additionalEditors {
		if err := r(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// ClientWithResponses builds on ClientInterface to offer response payloads
type ClientWithResponses struct {
	ClientInterface
}

// NewClientWithResponses creates a new ClientWithResponses, which wraps
// Client with return type handling
func NewClientWithResponses(server string, opts ...ClientOption) (*ClientWithResponses, error) {
	client, err := NewClient(server, opts...)
	if err != nil {
		return nil, err
	}
	return &ClientWithResponses{client}, nil
}

// WithBaseURL overrides the baseURL.
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) error {
		newBaseURL, err := url.Parse(baseURL)
		if err != nil {
			return err
		}
		c.Server = newBaseURL.String()
		return nil
	}
}

// ClientWithResponsesInterface is the interface specification for the client with responses above.
type ClientWithResponsesInterface interface {
	GetIndexedRouteMapWithResponse(ctx context.Context, params *GetIndexedRouteMapParams, reqEditors ...RequestEditorFn) (*GetIndexedRouteMapResponse, error)
	GetProgramIdToLabelWithResponse(ctx context.Context, reqEditors ...RequestEditorFn) (*GetProgramIdToLabelResponse, error)
	GetQuoteWithResponse(ctx context.Context, params *GetQuoteParams, reqEditors ...RequestEditorFn) (*GetQuoteResponse, error)
	PostSwapWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*PostSwapResponse, error)
	PostSwapWithResponse(ctx context.Context, body PostSwapJSONRequestBody, reqEditors ...RequestEditorFn) (*PostSwapResponse, error)
	PostSwapInstructionsWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*PostSwapInstructionsResponse, error)
	PostSwapInstructionsWithResponse(ctx context.Context, body PostSwapInstructionsJSONRequestBody, reqEditors ...RequestEditorFn) (*PostSwapInstructionsResponse, error)
	GetTokensWithResponse(ctx context.Context, reqEditors ...RequestEditorFn) (*GetTokensResponse, error)
}

type GetIndexedRouteMapResponse struct {
	Body         []byte
	HTTPResponse *http.Response
	JSON200      *IndexedRouteMapResponse
}

// Status returns HTTPResponse.Status
func (r GetIndexedRouteMapResponse) Status() string {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.Status
	}
	return http.StatusText(0)
}

// StatusCode returns HTTPResponse.StatusCode
func (r GetIndexedRouteMapResponse) StatusCode() int {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.StatusCode
	}
	return 0
}

type GetProgramIdToLabelResponse struct {
	Body         []byte
	HTTPResponse *http.Response
	JSON200      *map[string]string
}

// Status returns HTTPResponse.Status
func (r GetProgramIdToLabelResponse) Status() string {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.Status
	}
	return http.StatusText(0)
}

// StatusCode returns HTTPResponse.StatusCode
func (r GetProgramIdToLabelResponse) StatusCode() int {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.StatusCode
	}
	return 0
}

type GetQuoteResponse struct {
	Body         []byte
	HTTPResponse *http.Response
	JSON200      *QuoteResponse
}

// Status returns HTTPResponse.Status
func (r GetQuoteResponse) Status() string {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.Status
	}
	return http.StatusText(0)
}

// StatusCode returns HTTPResponse.StatusCode
func (r GetQuoteResponse) StatusCode() int {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.StatusCode
	}
	return 0
}

type PostSwapResponse struct {
	Body         []byte
	HTTPResponse *http.Response
	JSON200      *SwapResponse
}

// Status returns HTTPResponse.Status
func (r PostSwapResponse) Status() string {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.Status
	}
	return http.StatusText(0)
}

// StatusCode returns HTTPResponse.StatusCode
func (r PostSwapResponse) StatusCode() int {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.StatusCode
	}
	return 0
}

type PostSwapInstructionsResponse struct {
	Body         []byte
	HTTPResponse *http.Response
	JSON200      *SwapInstructionsResponse
}

// Status returns HTTPResponse.Status
func (r PostSwapInstructionsResponse) Status() string {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.Status
	}
	return http.StatusText(0)
}

// StatusCode returns HTTPResponse.StatusCode
func (r PostSwapInstructionsResponse) StatusCode() int {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.StatusCode
	}
	return 0
}

type GetTokensResponse struct {
	Body         []byte
	HTTPResponse *http.Response
	JSON200      *[]string
}

// Status returns HTTPResponse.Status
func (r GetTokensResponse) Status() string {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.Status
	}
	return http.StatusText(0)
}

// StatusCode returns HTTPResponse.StatusCode
func (r GetTokensResponse) StatusCode() int {
	if r.HTTPResponse != nil {
		return r.HTTPResponse.StatusCode
	}
	return 0
}

// GetIndexedRouteMapWithResponse request returning *GetIndexedRouteMapResponse
func (c *ClientWithResponses) GetIndexedRouteMapWithResponse(ctx context.Context, params *GetIndexedRouteMapParams, reqEditors ...RequestEditorFn) (*GetIndexedRouteMapResponse, error) {
	rsp, err := c.GetIndexedRouteMap(ctx, params, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParseGetIndexedRouteMapResponse(rsp)
}

// GetProgramIdToLabelWithResponse request returning *GetProgramIdToLabelResponse
func (c *ClientWithResponses) GetProgramIdToLabelWithResponse(ctx context.Context, reqEditors ...RequestEditorFn) (*GetProgramIdToLabelResponse, error) {
	rsp, err := c.GetProgramIdToLabel(ctx, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParseGetProgramIdToLabelResponse(rsp)
}

// GetQuoteWithResponse request returning *GetQuoteResponse
func (c *ClientWithResponses) GetQuoteWithResponse(ctx context.Context, params *GetQuoteParams, reqEditors ...RequestEditorFn) (*GetQuoteResponse, error) {
	rsp, err := c.GetQuote(ctx, params, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParseGetQuoteResponse(rsp)
}

// PostSwapWithBodyWithResponse request with arbitrary body returning *PostSwapResponse
func (c *ClientWithResponses) PostSwapWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*PostSwapResponse, error) {
	rsp, err := c.PostSwapWithBody(ctx, contentType, body, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParsePostSwapResponse(rsp)
}

func (c *ClientWithResponses) PostSwapWithResponse(ctx context.Context, body PostSwapJSONRequestBody, reqEditors ...RequestEditorFn) (*PostSwapResponse, error) {
	rsp, err := c.PostSwap(ctx, body, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParsePostSwapResponse(rsp)
}

// PostSwapInstructionsWithBodyWithResponse request with arbitrary body returning *PostSwapInstructionsResponse
func (c *ClientWithResponses) PostSwapInstructionsWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...RequestEditorFn) (*PostSwapInstructionsResponse, error) {
	rsp, err := c.PostSwapInstructionsWithBody(ctx, contentType, body, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParsePostSwapInstructionsResponse(rsp)
}

func (c *ClientWithResponses) PostSwapInstructionsWithResponse(ctx context.Context, body PostSwapInstructionsJSONRequestBody, reqEditors ...RequestEditorFn) (*PostSwapInstructionsResponse, error) {
	rsp, err := c.PostSwapInstructions(ctx, body, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParsePostSwapInstructionsResponse(rsp)
}

// GetTokensWithResponse request returning *GetTokensResponse
func (c *ClientWithResponses) GetTokensWithResponse(ctx context.Context, reqEditors ...RequestEditorFn) (*GetTokensResponse, error) {
	rsp, err := c.GetTokens(ctx, reqEditors...)
	if err != nil {
		return nil, err
	}
	return ParseGetTokensResponse(rsp)
}

// ParseGetIndexedRouteMapResponse parses an HTTP response from a GetIndexedRouteMapWithResponse call
func ParseGetIndexedRouteMapResponse(rsp *http.Response) (*GetIndexedRouteMapResponse, error) {
	bodyBytes, err := io.ReadAll(rsp.Body)
	defer func() { _ = rsp.Body.Close() }()
	if err != nil {
		return nil, err
	}

	response := &GetIndexedRouteMapResponse{
		Body:         bodyBytes,
		HTTPResponse: rsp,
	}

	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 200:
		var dest IndexedRouteMapResponse
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.JSON200 = &dest

	}

	return response, nil
}

// ParseGetProgramIdToLabelResponse parses an HTTP response from a GetProgramIdToLabelWithResponse call
func ParseGetProgramIdToLabelResponse(rsp *http.Response) (*GetProgramIdToLabelResponse, error) {
	bodyBytes, err := io.ReadAll(rsp.Body)
	defer func() { _ = rsp.Body.Close() }()
	if err != nil {
		return nil, err
	}

	response := &GetProgramIdToLabelResponse{
		Body:         bodyBytes,
		HTTPResponse: rsp,
	}

	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 200:
		var dest map[string]string
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.JSON200 = &dest

	}

	return response, nil
}

// ParseGetQuoteResponse parses an HTTP response from a GetQuoteWithResponse call
func ParseGetQuoteResponse(rsp *http.Response) (*GetQuoteResponse, error) {
	bodyBytes, err := io.ReadAll(rsp.Body)
	defer func() { _ = rsp.Body.Close() }()
	if err != nil {
		return nil, err
	}

	response := &GetQuoteResponse{
		Body:         bodyBytes,
		HTTPResponse: rsp,
	}

	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 200:
		var dest QuoteResponse
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.JSON200 = &dest

	}

	return response, nil
}

// ParsePostSwapResponse parses an HTTP response from a PostSwapWithResponse call
func ParsePostSwapResponse(rsp *http.Response) (*PostSwapResponse, error) {
	bodyBytes, err := io.ReadAll(rsp.Body)
	defer func() { _ = rsp.Body.Close() }()
	if err != nil {
		return nil, err
	}

	response := &PostSwapResponse{
		Body:         bodyBytes,
		HTTPResponse: rsp,
	}

	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 200:
		var dest SwapResponse
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.JSON200 = &dest

	}

	return response, nil
}

// ParsePostSwapInstructionsResponse parses an HTTP response from a PostSwapInstructionsWithResponse call
func ParsePostSwapInstructionsResponse(rsp *http.Response) (*PostSwapInstructionsResponse, error) {
	bodyBytes, err := io.ReadAll(rsp.Body)
	defer func() { _ = rsp.Body.Close() }()
	if err != nil {
		return nil, err
	}

	response := &PostSwapInstructionsResponse{
		Body:         bodyBytes,
		HTTPResponse: rsp,
	}

	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 200:
		var dest SwapInstructionsResponse
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.JSON200 = &dest

	}

	return response, nil
}

// ParseGetTokensResponse parses an HTTP response from a GetTokensWithResponse call
func ParseGetTokensResponse(rsp *http.Response) (*GetTokensResponse, error) {
	bodyBytes, err := io.ReadAll(rsp.Body)
	defer func() { _ = rsp.Body.Close() }()
	if err != nil {
		return nil, err
	}

	response := &GetTokensResponse{
		Body:         bodyBytes,
		HTTPResponse: rsp,
	}

	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 200:
		var dest []string
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.JSON200 = &dest

	}

	return response, nil
}
