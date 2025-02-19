# solana wallet tacker

A Solana wallet tracker that scan wallets and print token statistics.
Filter results with Include, Exclude lists, token Balance and or USD Value.

## Futures
- custom rpc
- track multiple wallets
- filter token by balance
- filter token by USD value
- filter include/exclude list
- wallet name (if any) -- does not work yet!
- token metadata, ticker, name, price, decimal etc.
- token change signed by wallet, or did someone transfered it
- include sol balance
- save token and wallet data
- retry and limiter for http calls
- debug

TODO:
- add websocket
- copy trade wallet:
  - set trade size
  - set trade vs token (default: sol)
  - set trade amount
  - set sell rules (default: follow wallet activity)

### Usage

```bash
$ ./walletTracker -h 
Usage ./walletTracker <FILE> [OPTIONAL] ...

Required:
	<FILE>                     Path to configuration file

Optional:
	--signer bool              Shows if balance change was made by wallet owner (default: true)
	--sol  bool                Include SOL balance (default: false)
	--debug  bool              Debug mode (default: false)
	-h,--help                  Show this help message

Example:
	./walletTracker wallet.config.json --signer=false --debug

```
```bash
./walletTracker dave_portnoy.json
2025/02/19 18:44:39 Loaded app settings from 'dave_portnoy.json'.
2025/02/19 18:44:39 Loaded token store from 'token_data.json'.
2025/02/19 18:44:39 Initalizing wallets and fetching token metadata.
2025/02/19 18:44:39 This process may take longer for wallets with a large number of tokens...
2025/02/19 18:44:47 Update> 5rkPDK4JnVAumgzeV2Zu8vjggMTtHdDtrsd5o9dhGZHD (238 tokens) in 8s.
2025/02/19 18:44:47 5rkP..> EMA           60793000      $5054405465815 3FQaXsbLrwPiMnWZkx7w3QcY5HYvBEKcJsfBfGhGRQUi
2025/02/19 18:44:47 5rkP..> HallaTomas    25000000      $907075146651  8MnF4AJbY2wGqkeqFXixBYKhU81Gs4c4hueJFeS8YKMd
2025/02/19 18:44:47 5rkP..> Putin         300000000     $612330540520  EaNirdXSTRFus3WvBnfHN5Zn85sNZnP9ekgLQxfGkr3o
2025/02/19 18:44:47 5rkP..> Barstools     915000000     $22243386343   7Zm96XEh1onLDnF4NEDNefvnNFvYBxcGRCwoX449NJfs
2025/02/19 18:44:47 5rkP..> PI            800000000     $14112825866   5GjWhPggud1NUGbejPGghRVusfRh1uGPX8ePGHPiY7ej
2025/02/19 18:44:47 5rkP..> Barron        44182850      $759273296     GNYkNA2ibw6MP4HGgBJg6EvspqH1oQteB5HjUodogM98
2025/02/19 18:44:47 5rkP..> DAVE          500000000     $382031500     9a3xSX8hTTCfD6Z4CeswpDT4iACfFyxo3DugBtkfvk2L
2025/02/19 18:44:47 5rkP..> PIZZA         950100000     $69067300      ErMCPhwmpbS8erVTJwy8D6qNJjsqy9wdV6RyhpUGKbij
2025/02/19 18:44:47 5rkP..> Freetool      930000000     $58743674      FMvZph9UyckDcgyMXzqvrH7tdVEa9Kcaf7nHxzeEkR7b
2025/02/19 18:44:47 5rkP..> SHORTNOY      17295708      $28268313      DNBXFzxfV9hqf9fZD7Mvm8aYnuZXuU6kGRV2nRRep1eL
2025/02/19 18:44:47 5rkP..> ... and 228 more tokens ...
2025/02/19 18:44:47 Scanning wallet balances every 1m0s...
2025/02/19 18:50:55 5rkP..> ⛳🍺          420690        $0             3sUdd48Xuq2dtCnuprKK2fYAcQK7CYiAkfYQMPAgpump <NEW>
2025/02/19 18:52:08 5rkP..> ⛳🍺 transaction NOT signed by wallet! probobly recived token.
```

### Create your app configuration file (required)
```json
{
  "CustomRPC": "https://api.mainnet-beta.solana.com",
  "UpdateInterval": "2m",
  "MaxRetries": 5,
  "Wallets": [
    "AZzYdTu9moqQsYeV4e1mzWLEdQc15BTuRWivjmPMY2S2"
  ],
  "MinimumBalance": 1,
  "MinimumValueUSD": 0,
  "IncludeTokenList": [],
  "ExcludeTokenList": []
}
```
