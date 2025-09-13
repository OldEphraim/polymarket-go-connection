#!/usr/bin/env python3
"""
Search Polymarket using the public-search API endpoint

Usage:
    python polymarket_public_search.py <query> [options]
    
Examples:
    python polymarket_public_search.py bitcoin
    python polymarket_public_search.py "super bowl"
    python polymarket_public_search.py election --limit 5
"""

import sys
import json
import argparse
import requests
from tabulate import tabulate
from typing import Dict, List, Optional

def search_polymarket(
    query: str,
    limit_per_type: int = 10,
    keep_closed_markets: int = 0,
    search_tags: bool = True,
    search_profiles: bool = False,
    events_tag: List[str] = None,
    events_status: str = None,
    sort: str = None,
    ascending: bool = False
) -> Dict:
    """
    Search Polymarket using the public-search endpoint
    
    Args:
        query: Search query string (required)
        limit_per_type: Max results per type
        keep_closed_markets: 0 or 1
        search_tags: Include tags in results
        search_profiles: Include profiles in results
        events_tag: Filter by event tags
        events_status: Filter by event status
        sort: Sort field
        ascending: Sort order
    
    Returns:
        Dictionary with events, tags, and profiles
    """
    
    url = "https://gamma-api.polymarket.com/public-search"
    
    # Build query parameters exactly as documented
    params = {
        "q": query,  # Required parameter
        "limit_per_type": limit_per_type,
        "keep_closed_markets": keep_closed_markets,
        "search_tags": search_tags,
        "search_profiles": search_profiles,
        "ascending": ascending
    }
    
    # Add optional parameters only if provided
    if events_tag:
        params["events_tag"] = events_tag
    if events_status:
        params["events_status"] = events_status
    if sort:
        params["sort"] = sort
    
    headers = {
        "Accept": "application/json",
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
    }
    
    try:
        print(f"Calling: {url}?q={query}&limit_per_type={limit_per_type}", file=sys.stderr)
        response = requests.get(url, params=params, headers=headers, timeout=15)
        response.raise_for_status()
        data = response.json()
        print(f"Response: {len(data.get('events', []))} events, {len(data.get('tags', []))} tags", file=sys.stderr)
        return data
    except requests.exceptions.RequestException as e:
        print(f"Error: {e}", file=sys.stderr)
        return {"events": [], "tags": [], "profiles": []}

def parse_market_from_event(event: Dict, market: Dict) -> Dict:
    """Extract and parse market data from within an event"""
    
    # Helper to safely parse numeric values
    def safe_float(value, default=0):
        if value is None:
            return default
        if isinstance(value, (int, float)):
            return float(value)
        if isinstance(value, str):
            try:
                return float(value)
            except:
                return default
        return default
    
    # Helper to safely parse JSON strings
    def safe_json(value, default=None):
        if default is None:
            default = []
        if isinstance(value, list):
            return value
        if isinstance(value, str):
            try:
                return json.loads(value)
            except:
                return default
        return default
    
    return {
        'question': market.get('question', ''),
        'slug': market.get('slug', ''),
        'market_id': market.get('id', ''),
        'condition_id': market.get('conditionId', ''),
        'active': market.get('active', False),
        'closed': market.get('closed', False),
        'accepting_orders': market.get('acceptingOrders', False),
        'volume': safe_float(market.get('volume')),
        'volume24hr': safe_float(market.get('volume24hr')),
        'liquidity': safe_float(market.get('liquidity')),
        'outcomes': safe_json(market.get('outcomes')),
        'outcome_prices': safe_json(market.get('outcomePrices')),
        'token_ids': safe_json(market.get('clobTokenIds')),
        'event_title': event.get('title', ''),
        'event_slug': event.get('slug', ''),
        'category': market.get('category', '') or event.get('category', ''),
        'created': market.get('createdAt', ''),
        'ends': market.get('endDate', '')
    }

def format_markets_table(markets: List[Dict], limit: int = 20) -> str:
    """Format markets for display"""
    
    if not markets:
        return "No markets found"
    
    table_data = []
    for i, market in enumerate(markets[:limit], 1):
        # Truncate question
        question = market['question']
        if len(question) > 45:
            question = question[:42] + '...'
        
        # Status
        if market['accepting_orders']:
            status = '✅'
        elif market['active'] and not market['closed']:
            status = '🟡'
        else:
            status = '❌'
        
        # Format numbers
        vol = market['volume24hr']
        if vol >= 1000000:
            vol_str = f"${vol/1000000:.1f}M"
        elif vol >= 1000:
            vol_str = f"${vol/1000:.0f}K"
        else:
            vol_str = f"${vol:.0f}"
        
        liq = market['liquidity']
        if liq >= 1000000:
            liq_str = f"${liq/1000000:.1f}M"
        elif liq >= 1000:
            liq_str = f"${liq/1000:.0f}K"
        else:
            liq_str = f"${liq:.0f}"
        
        # Get prices if binary market
        price_str = '-'
        if market['outcome_prices'] and len(market['outcome_prices']) >= 2:
            try:
                yes = float(market['outcome_prices'][0]) * 100
                no = float(market['outcome_prices'][1]) * 100
                price_str = f"{yes:.0f}/{no:.0f}"
            except:
                pass
        
        table_data.append([i, question, vol_str, liq_str, price_str, status])
    
    headers = ['#', 'Market', '24h Vol', 'Liquidity', 'Y/N%', '✓']
    return tabulate(table_data, headers=headers, tablefmt='grid')

def main():
    parser = argparse.ArgumentParser(description='Search Polymarket using public-search API')
    parser.add_argument('query', help='Search query (required)')
    parser.add_argument('--limit', type=int, default=10,
                       help='Results per type (default: 10)')
    parser.add_argument('--include-closed', action='store_true',
                       help='Include closed markets')
    parser.add_argument('--json', action='store_true',
                       help='Output as JSON')
    parser.add_argument('--details', action='store_true',
                       help='Show detailed information')
    
    args = parser.parse_args()
    
    # Perform search
    keep_closed = 1 if args.include_closed else 0
    
    results = search_polymarket(
        query=args.query,
        limit_per_type=args.limit,
        keep_closed_markets=keep_closed,
        search_tags=True,
        search_profiles=False
    )
    
    # Extract markets from events
    all_markets = []
    events = results.get('events', [])
    
    for event in events:
        event_markets = event.get('markets', [])
        for market in event_markets:
            parsed = parse_market_from_event(event, market)
            all_markets.append(parsed)
    
    # Sort by 24hr volume
    all_markets.sort(key=lambda x: x['volume24hr'], reverse=True)
    
    if args.json:
        # JSON output
        output = {
            'query': args.query,
            'events_found': len(events),
            'markets_found': len(all_markets),
            'markets': all_markets,
            'tags': results.get('tags', [])
        }
        print(json.dumps(output, indent=2, default=str))
    else:
        # Text output
        print("\n" + "="*80)
        print(f" POLYMARKET SEARCH: '{args.query}' ".center(80))
        print("="*80)
        print(f"Found: {len(events)} event(s), {len(all_markets)} market(s)")
        
        # Show tags if found
        tags = results.get('tags', [])
        if tags:
            tag_names = [t.get('label', '') for t in tags[:5]]
            print(f"Related tags: {', '.join(tag_names)}")
        
        if all_markets:
            print("\n" + format_markets_table(all_markets, limit=20))
            
            # Show details for top market
            if args.details and all_markets:
                top = all_markets[0]
                print("\n" + "="*80)
                print("TOP MARKET DETAILS:")
                print("="*80)
                print(f"Question: {top['question']}")
                print(f"Event: {top['event_title']}")
                print(f"Slug: {top['slug']}")
                print(f"24h Volume: ${top['volume24hr']:,.0f}")
                print(f"Liquidity: ${top['liquidity']:,.0f}")
                print(f"Accepting Orders: {top['accepting_orders']}")
                
                if top['outcomes'] and top['outcome_prices']:
                    print("\nOutcomes:")
                    for i, (outcome, price) in enumerate(zip(top['outcomes'], top['outcome_prices'])):
                        try:
                            p = float(price) * 100
                            print(f"  {i}. {outcome}: {p:.1f}¢")
                        except:
                            print(f"  {i}. {outcome}: [error]")
                
                if top['token_ids']:
                    print("\nToken IDs:")
                    for i, tid in enumerate(top['token_ids']):
                        print(f"  {i}: {tid}")
                
                print("\n" + "="*80)
                print("ACTIONS:")
                print("="*80)
                print(f"# Get market details:")
                print(f"python utils/get_token_from_slug.py \"{top['slug']}\"")
                
                if top['token_ids'] and len(top['token_ids']) > 0:
                    print(f"\n# Check orderbook:")
                    print(f"python utils/orderbook_analyzer.py --token-id {top['token_ids'][0]}")
                
                print(f"\n# Simulate trade:")
                print(f"python core/place_trade.py --slug \"{top['slug']}\" --side buy --amount 10 --outcome 0 --dry-run")
        else:
            print("\nNo markets found. Try:")
            print("  - Different search terms")
            print("  - Adding --include-closed")
            print("  - Checking if the API is working")

if __name__ == "__main__":
    main()
