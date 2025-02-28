# solana wallet tacker

A Solana wallet tracker that subscribes to wallet account and look for changes.
Filter results with Include, Exclude lists, token Balance and or USD Value.

## Futures
- custom rpc
- custom ws
- rate limiter
- retry on request error
- tracks multiple wallets
- filter changes made only by wallet owner
- filter by account change percent
- filter by account change value USD
- filter include/exclude token list
- token metadata, and realtime price
- save token and wallet data        -- needs update
- debug

TODO:
- fix copytrade .. not working well.
- wallet name (if any)              -- not implemented

### Usage

```bash
$ ./wallet_tracker -h 
Usage ./wallet_tracker <FILE> [OPTIONAL] ...

Required:
    <FILE>               Path to configuration file

Optional:
    --copytrade          Buy and sell token swaps signed by wallets.
    --balance bool       Show token balance on program start (default: false).
    --all bool           Show all token transactions accociated with account wallet (default: false).
    --verbose  bool      Show all messages (default: false).
    --help,-h            Show this help message.

Example:
    ./wallet_tracker wallet.config.json --all --balance -verbose

```
```bash
$ ./wallet_tracker app.config.json --all --balance -verbose
2025/02/23 02:26:08 Verbose is enabled.
2025/02/23 02:26:08 Showing all transactinos accosiated with account wallet.
2025/02/23 02:26:08 Loaded app settings from 'test.json'.
2025/02/23 02:26:08 Loaded token store from 'token_data.json'.
2025/02/23 02:26:08 Downloading token metadata for 1 wallet accounts.
2025/02/23 02:26:08 Fetched 5 token accounts for program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA
2025/02/23 02:26:08 Fetched 0 token accounts for program TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb
2025/02/23 02:26:08 Updated 5cPP5n3SUTf6CSgkLhSPY2VNhMiWwzpNBtAH5R7rUPhB (6 tokens) in 0s.
2025/02/23 02:26:08 5cPP> USDC          24813         $24814          EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v
2025/02/23 02:26:08 5cPP> JupSOL        100           $18628          jupSoLaHXQiZZTSfEWMTRRgpnyFm8f6sZdosWBjx93v
2025/02/23 02:26:08 5cPP> SOL           0             $57             So11111111111111111111111111111111111111112
2025/02/23 02:26:08 5cPP> RAY           2             $10             4k3Dyjzvzp8eMZWUXbBCjEvwSkkk59S5iCNLY3QrkX6R
2025/02/23 02:26:08 5cPP> HOPE          12148         $0              3zAjZYeTtMxrEFfjuoweJ4mKfr6FvLBreCNxi3duKsas
2025/02/23 02:26:08 5cPP> K9            239           $0              4kGcHzt91xVk6hyJYBK1yuF6sGCjeJ9PXed3G7pw9rtY
2025/02/23 02:26:09 Established connection to wss://solana-api.projectserum.com.
2025/02/23 02:26:09 Subscribed to transaction logs for wallet account 8cui5n3SUTf6CSgkLhSPY2VNhMiWwzpNBtAH5R7rUPhB
2025/02/23 02:26:37 5cPP> Tx: kmyhiZazL1kYS4PrgmX67zJL4BG8hYNwHzCdAstfmHNwHhiMGTqgkXQrmGz4f4BWXsrDkRpVCCkXQrx5SzMVaFBs
2025/02/23 02:26:37 5cPP> USDC         +10            $+10            EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v   SWAP  
2025/02/23 02:26:38 5cPP> RAY          -2             $-10            4k3Dyjzvzp8eMZWUXbBCjEvwSkkk59S5iCNLY3QrkX6R   SOLD ALL
2025/02/23 02:26:42 5cPP> Tx: ff9Cstub1Mt2TpMuJSbXJj6dz3eC4aCMPRYifr6F8oyQv2FG1sqoMjeHDLSatC6VGEWwtjNgWkd3CJT1VoEQEup
2025/02/23 02:26:42 5cPP> Transaction is not signed by this wallet account!
2025/02/23 03:02:49 5cPP> shortnoy     +6.900158      $+0.000058     A4PWgKGXSPYnjk9ZkTbXhJASUpCqgUSHVTbvPruPpump
```

### Create your app configuration file (required)
```json
i{
  "NETWORK": {
    "CustomRPC": "https://api.mainnet-beta.solana.com",
    "CustomWS": ""
  },

  "MONITOR": {
    "Wallets": [
      "5cPP5n3SUTf6CSgkLhSPY2VNhMiWwzpNBtAH5R7rUPhB"
    ],
    "ChangePercent": 0,
    "ChangeValueUSD": 0,
    "TokenBalance": 0,
    "TokenValueUSD": 0
  },
  "TOKEN_FILTER": {
    "IncludeTokenList": [],
    "ExcludeTokenList": []
  },
  "ACTION_WALLET": {
    "PublicKey": "8D2m4s5EuaDHfBqrBJFZMadVQYe41Eh9jyJWq4mfBFeeR",
    "PrivateKey": "2gFTtm8mbYErPLJkRx6SV6K3q7PYR7mbRgnMWoX9YWhvZsuunSQE1RN28fDXyCPJjPtJ7FPeDgxSZFeQWFuuoqSm",
    "BuyMint": "So11111111111111111111111111111111111111112",
    "BuyAmount": 0.1,
    "SellMint": "So11111111111111111111111111111111111111112"
  }
}
```
