#!/usr/bin/env python3
"""
Search for Polymarket markets by keyword

Usage:
    python search_markets.py <keyword> [--active-only] [--min-volume N] [--json]
    
Examples:
    python search_markets.py bitcoin
    python search_markets.py "super bowl" --active-only
    python search_markets.py election --min-volume 10000
"""

import sys
import json
import argparse
import requests
from tabulate import tabulate

def search_markets(keyword, active_only=False, min_volume=0):
    """Search for markets containing a keyword"""
    
    # Try different API endpoints
    apis = [
        "https://gamma-api.polymarket.com/markets",
        "https://clob.polymarket.com/markets"
    ]
    
    all_markets = []
    
    for api in apis:
        try:
            response = requests.get(api, timeout=10)
            if response.status_code == 200:
                data = response.json()
                
                # Handle different response formats
                if isinstance(data, dict) and 'data' in data:
                    markets = data['data']
                elif isinstance(data, list):
                    markets = data
                else:
                    continue
                
                all_markets.extend(markets)
                break  # Use first successful API
        except:
            continue
    
    if not all_markets:
        return []
    
    # Filter markets
    matches = []
    seen_slugs = set()  # Avoid duplicates
    
    for market in all_markets:
        if isinstance(market, dict):
            slug = market.get('slug', '')
            
            # Skip if we've seen this market
            if slug in seen_slugs:
                continue
            seen_slugs.add(slug)
            
            # Search in question/title
            question = market.get('question', '').lower()
            title = market.get('title', '').lower()
            
            if keyword.lower() not in question and keyword.lower() not in title:
                continue
            
            # Check filters
            is_active = market.get('active', False)
            is_closed = market.get('closed', False)
            accepting = market.get('acceptingOrders', False)
            volume = float(market.get('volume24hr', 0))
            
            if active_only and (not is_active or is_closed or not accepting):
                continue
            
            if volume < min_volume:
                continue
            
            matches.append({
                'question': market.get('question', ''),
                'slug': slug,
                'active': is_active,
                'closed': is_closed,
                'accepting': accepting,
                'volume': volume,
                'liquidity': float(market.get('liquidity', 0)),
                'created': market.get('createdAt', ''),
                'ends': market.get('endDate', '')
            })
    
    # Sort by volume (most liquid first)
    matches.sort(key=lambda x: x['volume'], reverse=True)
    
    return matches

def format_markets(markets, format_type='table'):
    """Format market results for display"""
    
    if format_type == 'json':
        return json.dumps(markets, indent=2, default=str)
    
    if not markets:
        return "No markets found matching criteria"
    
    # Prepare for table display
    table_data = []
    
    for i, market in enumerate(markets[:20], 1):  # Limit to 20 for display
        # Truncate question for display
        question = market['question']
        if len(question) > 50:
            question = question[:47] + '...'
        
        # Format status
        if market['active'] and not market['closed'] and market['accepting']:
            status = '✅ Active'
        elif market['closed']:
            status = '❌ Closed'
        else:
            status = '⚠️  Inactive'
        
        table_data.append([
            i,
            question,
            f"${market['volume']:,.0f}",
            f"${market['liquidity']:,.0f}",
            status,
            market['slug'][:30] + '...' if len(market['slug']) > 30 else market['slug']
        ])
    
    headers = ['#', 'Market', '24h Volume', 'Liquidity', 'Status', 'Slug']
    return tabulate(table_data, headers=headers, tablefmt='grid')

def main():
    parser = argparse.ArgumentParser(description='Search Polymarket markets')
    parser.add_argument('keyword', help='Search keyword or phrase')
    parser.add_argument('--active-only', action='store_true', 
                       help='Only show active markets accepting orders')
    parser.add_argument('--min-volume', type=float, default=0,
                       help='Minimum 24h volume in USD')
    parser.add_argument('--json', action='store_true',
                       help='Output as JSON')
    parser.add_argument('--limit', type=int, default=20,
                       help='Maximum results to show (default: 20)')
    
    args = parser.parse_args()
    
    # Search markets
    markets = search_markets(
        args.keyword,
        active_only=args.active_only,
        min_volume=args.min_volume
    )
    
    if args.json:
        # JSON output
        output = {
            'query': args.keyword,
            'total_found': len(markets),
            'markets': markets[:args.limit]
        }
        print(json.dumps(output, indent=2, default=str))
    else:
        # Text output
        print("\n" + "="*80)
        print(f" POLYMARKET SEARCH: '{args.keyword}' ".center(80))
        print("="*80)
        
        if args.active_only:
            print("Filter: Active markets only")
        if args.min_volume > 0:
            print(f"Filter: Minimum volume ${args.min_volume:,.0f}")
        
        print(f"\nFound {len(markets)} market(s)")
        
        if markets:
            print("\n" + format_markets(markets[:args.limit]))
            
            # Show usage hint
            print("\n" + "="*80)
            print("TO GET TOKEN IDS:")
            print("="*80)
            print(f"python get_token_from_slug.py <slug>")
            print(f"\nExample:")
            if markets:
                print(f"python get_token_from_slug.py {markets[0]['slug']}")

if __name__ == "__main__":
    main()

