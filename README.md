# solana wallet tacker

A Solana wallet tracker that subscribes to wallet account and look for changes.
Filter results with Include, Exclude lists, token Balance and or USD Value.

## Futures
- blazing fast
- custom rpc
- custom ws
- rate limiter
- retry on error
- tracks multiple wallets
- filter changes made only by wallet owner
- filter by account change percent
- filter by account change value USD
- filter include/exclude token list
- wallet name (if any)              -- not implemented
- token metadata, and realtime price
- save token and wallet data        -- needs update
- debug

TODO:
- copy trade wallet:
  - set trade size
  - set trade vs token (default: sol)
  - set trade amount
  - set buy rules
    - buy once
    - dca in over N times over T duration
  - set sell rules
    - sell once
    - sell all after T seconds
    - dca out over N times over T duration

- more ?

### Usage

```bash
$ ./walletTracker -h 
Usage ./walletTracker <FILE> [OPTIONAL] ...

Required:
	<FILE>               Path to configuration file

Optional:
	--signed bool        Only show changes that is signed by wallet (default: false)
	--debug  bool        Debug mode (default: false)
	-h,--help            Show help message

Example:
	./walletTracker wallet.config.json --signed true --debug

```
```bash
2025/02/21 14:32:03 Loaded app settings from 'dave_portnoy.json'.
2025/02/21 14:32:03 Loaded token store from 'token_data.json'.
2025/02/21 14:32:03 Initalizing wallets and fetching token metadata.
2025/02/21 14:32:03 This process may take longer for wallets with a large number of tokens...
2025/02/21 14:32:14 Updated> 5rkPDK4JnVAumgzeV2Zu8vjggMTtHdDtrsd5o9dhGZHD (3525 tokens) in 9s.
2025/02/21 14:32:14 5rkP> HallaTomas    25000000      $949081404956  8MnF4AJbY2wGqkeqFXixBYKhU81Gs4c4hueJFeS8YKMd
2025/02/21 14:32:14 5rkP> Putin         300000000     $640687303406  EaNirdXSTRFus3WvBnfHN5Zn85sNZnP9ekgLQxfGkr3o
2025/02/21 14:32:14 5rkP> EMA           60793000      $589291051801  3FQaXsbLrwPiMnWZkx7w3QcY5HYvBEKcJsfBfGhGRQUi
2025/02/21 14:32:14 5rkP> OS            833333        $43859168897   5NFeJPEzquryBguZLz9uH2s2scN2ntWBH34o2zAwga9D
2025/02/21 14:32:14 5rkP> PI            800000000     $29063574788   5GjWhPggud1NUGbejPGghRVusfRh1uGPX8ePGHPiY7ej
2025/02/21 14:32:14 5rkP> Barstools     915000000     $23273467958   7Zm96XEh1onLDnF4NEDNefvnNFvYBxcGRCwoX449NJfs
2025/02/21 14:32:14 5rkP> Barron        44182850      $7933281389    GNYkNA2ibw6MP4HGgBJg6EvspqH1oQteB5HjUodogM98
2025/02/21 14:32:14 5rkP> DAVE          500000000     $634297000     9a3xSX8hTTCfD6Z4CeswpDT4iACfFyxo3DugBtkfvk2L
2025/02/21 14:32:14 5rkP> Freetool      930000000     $61464068      FMvZph9UyckDcgyMXzqvrH7tdVEa9Kcaf7nHxzeEkR7b
2025/02/21 14:32:14 5rkP> SHORTNOY      17295708      $29577408      DNBXFzxfV9hqf9fZD7Mvm8aYnuZXuU6kGRV2nRRep1eL
2025/02/21 14:32:14 5rkP> ... and 3515 more tokens ...
2025/02/21 14:32:14 Enstablished connection to wss://solana-api.projectserum.com
2025/02/21 14:32:14 Subscribe to account changes for wallet 5rkPDK4JnVAumgzeV2Zu8vjggMTtHdDtrsd5o9dhGZHD
2025/02/21 14:32:49 5rkP> UPDATE shortnoy     +6.900158      $+0.000058      A4PWgKGXSPYnjk9ZkTbXhJASUpCqgUSHVTbvPruPpump
2025/02/21 14:33:51 5rkP> UPDATE shortnoy     +6.900158      $+0.000058      A4PWgKGXSPYnjk9ZkTbXhJASUpCqgUSHVTbvPruPpump
2025/02/21 14:34:49 5rkP> UPDATE shortnoy     +6.900158      $+0.000058      A4PWgKGXSPYnjk9ZkTbXhJASUpCqgUSHVTbvPruPpump
```

### Create your app configuration file (required)
```json
{
  "CustomRPC": "https://api.mainnet-beta.solana.com",
  "CustomWS": "",
  "Wallets": [
    "5rkPDK4JnVAumgzeV2Zu8vjggMTtHdDtrsd5o9dhGZHD"
  ],
  "ChangePercent": 0,
  "ChangeValueUSD": 0,
  "IncludeTokenList": [],
  "ExcludeTokenList": []
}
```
