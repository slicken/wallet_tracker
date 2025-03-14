# solana wallet tacker

solana public       7iN5uMh33C1NSqr1JjwMDtfukfgUj6PnSqnkfzkRrpXP
solana private      24j3XQ7A9SvH2f2JRmmc9Q4W7tXfycHBc53Ln4m7cV62Xmg3KhPR64WZXUCotZhF41kv2PVWrCRNiC7b7tS2f96d
solana mnemonic     sniff virus vehicle subject sock conduct voice corn list hawk choice blush
solana derivation   m/44'/501'/0'/0'

A Solana wallet tracker that subscribes to wallet account and look for balance and token account changes.
Copytrade the wallets and apply filters and rules for how long we want to hold token, sell when owner does and more.

## Futures
- custom rpc
- custom ws
- rate limiter
- retry on rpc error
- tracks multiple wallets
- filter changes made only by wallet owner
- filter by account change percent
- filter by account change value USD
- filter include/exclude token list
- get token metadata, and realtime price
- copytrade wallets
- save wallet settngs

TODO:
- update copytrade functionality. add more options
- implement .sol wallet names for easier tracking

### Usage

```bash
$ ./wallet_tracker -h 
Usage ./wallet_tracker <FILE> [OPTIONAL] ...

Required:
    <FILE>               Path to configuration file

Optional:
    --copytrade bool     Buy and sell token swaps signed by wallets (default: false).
    --balance bool       Show token balance on program start (default: false).
    --verbose bool       Show all messages (default: false).
    --help,-h            Show this help message.

Example:
    ./wallet_tracker wallet.config.json --balance --copytrade

```
```bash
$ ./wallet_tracker app.config.json --balance --verbose
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
2025/02/23 03:02:49 5cPP> shortnoy     +6.900158      $+0.000058      A4PWgKGXSPYnjk9ZkTbXhJASUpCqgUSHVTbvPruPpump
```

### Create your app configuration file (required)
```json
{
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
    "PublicKey": "",
    "PrivateKey": "",
    "BuyMint": "So11111111111111111111111111111111111111112",
    "BuyAmount": 0.1,
    "SellMint": "So11111111111111111111111111111111111111112"
  }
}
```
