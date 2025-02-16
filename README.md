# solana wallet tacker !!UNDER DEVLOPEMENT!!

A Solana wallet tracker that scan wallets and print token statistics.
Filter results with Include, Exclude lists, token Balance and or USD Value.

## Futures
- custom rpc
- track multiple wallets
- filter token by minimum balance
- filter token by USD value
- filter Include, Exclude list
- save token and wallet data


TODO:
- add websocket
- copy wallet activity:
  - set trade size
  - set trade vs token (default: sol)
  - set maximum ongoing size
  - set sell rules (default: follow wallet activity)

### Usage

```bash
$ ./walletTracker --help
Usage of ./walletTracker:
  -config string
    	Path to configuration file (default "config.json")
  -debug
    	Debug mode
  -price
    	Fetch prices (needed to calculate USD value)
```
```bash
$ ./walletTracker --config=my_config.json --price=true
2025/02/16 09:48:59 Loaded app settings from 'config.new.json'
2025/02/16 09:48:59 Loaded token store from 'token_data.json'
2025/02/16 09:48:59 Fetching prices may take some time for wallet with many tokens...
2025/02/16 09:49:00 Wallet> AZzYdTu9moqQsYeV4e1mzWLEdQc15BTuRWivjmPMY2S2 (5 tokens) fetched in 1s
2025/02/16 09:49:00 Jail Milei      33562         54$ 6LYdA9RXGfGyg8enWE8pnYoCGgk7NtYr4dmMSqfxkdhc
2025/02/16 09:49:00 JupSOL             92      19382$ jupSoLaHXQiZZTSfEWMTRRgpnyFm8f6sZdosWBjx93v
2025/02/16 09:49:00 USD C              13         13$ 8GsjjKTBXff1deWBCWMWMVDiq2btRWeGxo38k5UHAX92
2025/02/16 09:49:00 Sorkincoin        450          7$ CHfSidPhzUEmu2ac8MHazDh9EEXYHezNxCHZ6YMtMMFZ
2025/02/16 09:49:00 SPX              1500       1123$ J3NKxxXZcnNiMjKw9hYb2K4LUxgwB6t1FtPtQVsv3KFr
2025/02/16 09:49:00 Scanning wallet balances every 30s...
2025/02/16 09:49:30 Wallet> AZzYdTu9moqQsYeV4e1mzWLEdQc15BTuRWivjmPMY2S2 (4 tokens) fetched in 1s
2025/02/16 09:49:30 Jail Milei     -33562        -54$ 6LYdA9RXGfGyg8enWE8pnYoCGgk7NtYr4dmMSqfxkdhc <REMOVED>
2025/02/16 09:49:30 TRUMP              +3        +54$ 6p6xgHyF7AeE6TZkSmFsko444wqoP15icUSqi2jfGiPN <NEW>
2025/02/16 09:49:30 Scanning wallet balances every 30s...
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
  "MinimumValueUSD": 99.5,
  "IncludeTokenList": [],
  "ExcludeTokenList": []
}
```
