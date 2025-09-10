#!/usr/bin/env python3
import requests
import json
import sys
import argparse

def get_market_by_slug(slug):
    """Get market info using the slug endpoint"""
    
    # Try different API endpoints
    endpoints = [
        f"https://gamma-api.polymarket.com/markets/slug/{slug}",
        f"https://gamma-api.polymarket.com/markets/{slug}",
        f"https://clob.polymarket.com/markets/{slug}"
    ]
    
    for url in endpoints:
        print(f"Trying: {url}")
        
        try:
            response = requests.get(url)
            
            if response.status_code == 200:
                data = response.json()
                
                # Check if we got market data
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
    
    print("\n" + "="*70)
    print("MARKET FOUND!")
    print("="*70)
    print(f"Question: {question}")
    print(f"Active: {active}")
    print(f"Closed: {closed}")
    print(f"Accepting Orders: {accepting}")
    
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
    if token_list:
        print("\n" + "="*70)
        print("TOKEN INFORMATION")
        print("="*70)
        
        for i in range(len(token_list)):
            outcome = outcome_list[i] if i < len(outcome_list) else f"Outcome {i+1}"
            try:
                price = float(price_list[i]) if i < len(price_list) else 0.0
            except:
                price = 0.0
            
            token_id = token_list[i]
            
            print(f"\n{i+1}. {outcome}")
            print(f"   Token ID: {token_id}")
            print(f"   Current Price: ${price:.4f} ({price*100:.2f}¢)")
            
            results.append({
                'outcome': outcome,
                'token_id': token_id,
                'price': price
            })
    else:
        print("\n⚠️  No token IDs found in market data")
    
    return results

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
    parser.add_argument('--raw', action='store_true', help='Show raw API response')
    
    args = parser.parse_args()
    
    # Extract slug from URL if needed
    slug = extract_slug_from_url(args.market)
    
    print(f"🔍 POLYMARKET TOKEN FINDER")
    print("="*70)
    print(f"Input: {args.market}")
    print(f"Extracted slug: {slug}")
    print("-"*70)
    
    # Get market data
    data, successful_url = get_market_by_slug(slug)
    
    if data:
        print(f"✅ Success with: {successful_url}")
        
        if args.raw:
            print("\nRaw API Response:")
            print(json.dumps(data, indent=2))
        
        # Parse and display tokens
        tokens = parse_market_data(data)
        
        if tokens:
            print("\n" + "="*70)
            print("COPY THIS FOR YOUR SCRIPT:")
            print("="*70)
            
            for token in tokens:
                print(f"\n# For {token['outcome']}:")
                print(f'TOKEN_ID = "{token["token_id"]}"')
                print(f'OUTCOME = "{token["outcome"]}"')
                print(f'CURRENT_PRICE = {token["price"]:.3f}  # {token["price"]*100:.1f}¢')
        
        # Check if market is tradeable
        if data.get('closed'):
            print("\n⚠️  WARNING: This market is CLOSED")
        if not data.get('acceptingOrders'):
            print("\n⚠️  WARNING: This market is NOT accepting orders")
            
    else:
        print(f"\n❌ Could not find market with slug: {slug}")
        print("\nPossible issues:")
        print("1. The market doesn't exist")
        print("2. The slug is incorrect")
        print("3. The market has been delisted")
        print("\nTry:")
        print("1. Check the URL is correct")
        print("2. Go to the market page and look for the slug in the URL")
        print("3. Try just the last part of the URL after the final /")

if __name__ == "__main__":
    main()

