#!/usr/bin/env python3
"""
Configuration constants for Polymarket trading system

This file contains hardcoded addresses and constants that don't change.
Private keys and wallet addresses should be in .env file.
"""

# Network Configuration
POLYGON_RPC = "https://polygon-rpc.com"
CHAIN_ID = 137

# Polymarket API Endpoints
CLOB_API = "https://clob.polymarket.com"
GAMMA_API = "https://gamma-api.polymarket.com"

# Contract Addresses (Polygon Mainnet)
CONTRACTS = {
    # USDC Tokens
    'USDC_BRIDGED': "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174",  # PoS Bridge USDC
    'USDC_NATIVE': "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359",    # Native USDC.e
    
    # Polymarket Contracts
    'CTF': "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045",             # Conditional Token Framework
    'CTF_EXCHANGE': "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E",    # Main exchange
    'NEG_RISK_EXCHANGE': "0xC5d563A36AE78145C45a50134d48A1215220f80a", # Neg risk markets
    'NEG_RISK_ADAPTER': "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296",  # Neg risk adapter
}

# Token Decimals
DECIMALS = {
    'USDC': 6,
    'CTF': 6,
    'MATIC': 18
}

# Trading Limits
LIMITS = {
    'MIN_ORDER_SIZE': 1.0,      # Minimum order size in USDC
    'MAX_PRICE': 0.99,          # Maximum price (99¢)
    'MIN_PRICE': 0.01,          # Minimum price (1¢)
    'PRICE_DECIMALS': 2,        # Max decimals for price
    'SIZE_DECIMALS': 4,         # Max decimals for size
}

# Gas Settings (in GWEI)
GAS_SETTINGS = {
    'DEFAULT': None,            # Use network default
    'SLOW': 30,
    'STANDARD': 50,
    'FAST': 100,
    'URGENT': 200
}

# Minimum Balances
MIN_BALANCES = {
    'MATIC': 0.1,              # Minimum MATIC for gas
    'USDC': 1.0                # Minimum USDC to trade
}

# API Rate Limits (requests per second)
RATE_LIMITS = {
    'CLOB_API': 10,
    'GAMMA_API': 10,
    'BLOCKCHAIN': 5
}

# Default Settings
DEFAULTS = {
    'ORDER_TYPE': 'GTC',       # Good Till Cancelled
    'SLIPPAGE': 0.01,          # 1% slippage for market orders
    'REFRESH_INTERVAL': 10,     # Seconds for monitoring
}

# Display Settings
DISPLAY = {
    'MAX_QUESTION_LENGTH': 80,
    'MAX_SLUG_LENGTH': 50,
    'DEFAULT_TABLE_LIMIT': 20,
    'DECIMAL_PLACES': 4
}

