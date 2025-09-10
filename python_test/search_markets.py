import requests
import json
import sys

def search_markets(keyword):
    """Search for markets containing a keyword"""
    
    print(f"Searching for markets containing: '{keyword}'")
    print("="*70)
    
    # Get all markets
    response = requests.get("https://gamma-api.polymarket.com/markets")
    
    if response.status_code != 200:
        print(f"Error fetching markets: {response.status_code}")
        return []
    
    markets = response.json()
    matches = []
    
    # Search for matching markets
    for market in markets:
        if isinstance(market, dict):
            question = market.get('question', '').lower()
            slug = market.get('slug', '')
            
            if keyword.lower() in question:
                is_active = market.get('active', False)
                is_closed = market.get('closed', False)
                accepting = market.get('acceptingOrders', False)
                
                matches.append({
                    'question': market.get('question', ''),
                    'slug': slug,
                    'active': is_active,
                    'closed': is_closed,
                    'accepting': accepting,
                    'volume': market.get('volume24hr', 0)
                })
    
    return matches

def main():
    if len(sys.argv) < 2:
        print("Usage: python search_markets.py <keyword>")
        print("Example: python search_markets.py ukraine")
        sys.exit(1)
    
    keyword = ' '.join(sys.argv[1:])
    
    print("🔍 MARKET SEARCH")
    print("="*70)
    
    matches = search_markets(keyword)
    
    if not matches:
        print(f"No markets found containing '{keyword}'")
        return
    
    print(f"Found {len(matches)} markets containing '{keyword}':\n")
    
    # Sort by volume
    matches.sort(key=lambda x: x['volume'], reverse=True)
    
    # Group by status
    active_markets = [m for m in matches if m['active'] and not m['closed'] and m['accepting']]
    inactive_markets = [m for m in matches if not (m['active'] and not m['closed'] and m['accepting'])]
    
    if active_markets:
        print("ACTIVE MARKETS (accepting orders):")
        print("-"*70)
        for i, market in enumerate(active_markets[:10], 1):
            print(f"\n{i}. {market['question']}")
            print(f"   Slug: {market['slug']}")
            print(f"   URL: https://polymarket.com/event/{market['slug']}")
            print(f"   Volume: ${market['volume']:,.0f}")
    
    if inactive_markets and len(active_markets) < 5:
        print("\n\nINACTIVE/CLOSED MARKETS:")
        print("-"*70)
        for i, market in enumerate(inactive_markets[:5], 1):
            print(f"\n{i}. {market['question']}")
            print(f"   Slug: {market['slug']}")
            print(f"   Status: Active={market['active']}, Closed={market['closed']}, Accepting={market['accepting']}")
    
    if active_markets:
        print("\n" + "="*70)
        print("To get token IDs for any market above:")
        print(f"python get_tokens.py <slug>")
        print(f"\nExample:")
        print(f"python get_tokens.py {active_markets[0]['slug']}")

if __name__ == "__main__":
    main()

