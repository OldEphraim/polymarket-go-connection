#!/usr/bin/env python3
"""
Get token IDs and market information from a Polymarket slug

Usage:
    python get_token_from_slug.py <market-slug> [--json]
    
Examples:
    python get_token_from_slug.py will-bitcoin-hit-100k-in-2024
    python get_token_from_slug.py will-bitcoin-hit-100k-in-2024 --json > market.json
"""

import sys
import json
import argparse
import requests

def get_market_by_slug(slug):
    """Get market info using the slug endpoint"""
    
    # Try different API endpoints in order of preference
    endpoints = [
        f"https://gamma-api.polymarket.com/markets/slug/{slug}",
        f"https://gamma-api.polymarket.com/markets/{slug}",
        f"https://clob.polymarket.com/markets/{slug}"
    ]
    
    for url in endpoints:
        try:
            response = requests.get(url, timeout=5)
            
            if response.status_code == 200:
                data = response.json()
                
                # Check if we got valid market data
                if 'question' in data or 'title' in data:
                    return data, url
                    
        except Exception as e:
            continue
    
    return None, None

def parse_market_data(data):
    """Parse market data regardless of format"""
    
    results = []
    
    # Get basic info
    question = data.get('question') or data.get('title') or 'Unknown Market'
    active = data.get('active', 'Unknown')
    closed = data.get('closed', 'Unknown')
    accepting = data.get('acceptingOrders', 'Unknown')

    # Handle volume and liquidity - they might be strings or missing
    try:
        volume = float(data.get('volume24hr', 0))
    except (TypeError, ValueError):
        volume = 0
    
    try:
        liquidity = float(data.get('liquidity', 0))
    except (TypeError, ValueError):
        liquidity = 0
    
    # Try to get token IDs
    clob_token_ids = data.get('clobTokenIds', '')
    outcomes = data.get('outcomes', '')
    outcome_prices = data.get('outcomePrices', '')
    
    # Parse tokens - handle different formats
    token_list = []
    outcome_list = []
    price_list = []
    
    # Check if we have the data in different formats
    if clob_token_ids:
        # Format 1: JSON array as string
        if isinstance(clob_token_ids, str) and clob_token_ids.startswith('['):
            try:
                token_list = json.loads(clob_token_ids)
                outcome_list = json.loads(outcomes) if outcomes else []
                price_list = json.loads(outcome_prices) if outcome_prices else []
            except:
                pass
        # Format 2: Comma-separated
        elif isinstance(clob_token_ids, str) and ',' in clob_token_ids:
            token_list = [t.strip() for t in clob_token_ids.split(',')]
            outcome_list = [o.strip() for o in outcomes.split(',')] if outcomes else []
            price_list = [p.strip() for p in outcome_prices.split(',')] if outcome_prices else []
        # Format 3: Already a list
        elif isinstance(clob_token_ids, list):
            token_list = clob_token_ids
            outcome_list = outcomes if isinstance(outcomes, list) else []
            price_list = outcome_prices if isinstance(outcome_prices, list) else []
    
    # Alternative: check for 'tokens' field (CLOB API format)
    elif 'tokens' in data:
        tokens = data['tokens']
        for token in tokens:
            token_list.append(token.get('token_id', ''))
            outcome_list.append(token.get('outcome', ''))
            price_list.append(str(token.get('price', 0)))
    
    # Build results
    market_info = {
        'question': question,
        'active': active,
        'closed': closed,
        'accepting_orders': accepting,
        'volume_24hr': volume,
        'liquidity': liquidity,
        'tokens': []
    }
    
    for i in range(len(token_list)):
        outcome = outcome_list[i] if i < len(outcome_list) else f"Outcome {i+1}"
        try:
            price = float(price_list[i]) if i < len(price_list) else 0.0
        except:
            price = 0.0
        
        token_id = token_list[i]
        
        market_info['tokens'].append({
            'outcome': outcome,
            'token_id': token_id,
            'price': price,
            'price_cents': price * 100
        })
    
    return market_info

def extract_slug_from_url(url):
    """Extract slug from a Polymarket URL"""
    # Remove trailing slash and query parameters
    url = url.rstrip('/').split('?')[0]
    
    # Get the last part of the URL
    parts = url.split('/')
    
    # Handle different URL formats
    if 'polymarket.com' in url:
        # For URLs like /event/slug or /market/slug
        return parts[-1]
    else:
        # Assume it's already a slug
        return url

def main():
    parser = argparse.ArgumentParser(description='Get Polymarket token IDs from URL or slug')
    parser.add_argument('market', help='Market URL or slug')
    parser.add_argument('--json', action='store_true', help='Output as JSON')
    parser.add_argument('--raw', action='store_true', help='Show raw API response')
    
    args = parser.parse_args()
    
    # Extract slug from URL if needed
    slug = extract_slug_from_url(args.market)
    
    # Get market data
    data, successful_url = get_market_by_slug(slug)
    
    if not data:
        if not args.json:
            print(f"❌ Could not find market with slug: {slug}")
            print("\nPossible issues:")
            print("1. The market doesn't exist")
            print("2. The slug is incorrect")
            print("3. The market has been delisted")
        else:
            print(json.dumps({'error': f'Market not found: {slug}'}, indent=2))
        sys.exit(1)
    
    if args.raw:
        print(json.dumps(data, indent=2))
        sys.exit(0)
    
    # Parse and display
    market_info = parse_market_data(data)
    
    if args.json:
        print(json.dumps(market_info, indent=2))
    else:
        print("\n" + "="*70)
        print("MARKET INFORMATION")
        print("="*70)
        print(f"Question: {market_info['question']}")
        print(f"Active: {market_info['active']}")
        print(f"Closed: {market_info['closed']}")
        print(f"Accepting Orders: {market_info['accepting_orders']}")
        print(f"24hr Volume: ${market_info['volume_24hr']:,.2f}")

        # Only show liquidity if it's available and non-zero
        if market_info.get('liquidity', 0) > 0:
            print(f"Liquidity: ${market_info['liquidity']:,.2f}")

        if market_info['tokens']:
            print("\n" + "="*70)
            print("TOKEN INFORMATION")
            print("="*70)
            
            for i, token in enumerate(market_info['tokens'], 1):
                print(f"\n{i}. {token['outcome']}")
                print(f"   Token ID: {token['token_id']}")
                print(f"   Current Price: ${token['price']:.4f} ({token['price_cents']:.1f}¢)")
            
            print("\n" + "="*70)
            print("COPY FOR TRADING:")
            print("="*70)
            
            for token in market_info['tokens']:
                print(f"\n# For {token['outcome']}:")
                print(f'TOKEN_ID = "{token["token_id"]}"')
                print(f'PRICE = {token["price"]:.3f}  # {token["price_cents"]:.1f}¢')
        
        # Warnings
        if market_info['closed']:
            print("\n⚠️  WARNING: This market is CLOSED")
        if not market_info['accepting_orders']:
            print("\n⚠️  WARNING: This market is NOT accepting orders")

if __name__ == "__main__":
    main()

