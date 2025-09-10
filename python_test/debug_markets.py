import requests
from datetime import datetime
import pytz

def debug_active_markets():
    """Find out what's going on with the markets"""
    
    print("="*70)
    print("INVESTIGATING POLYMARKET MARKET STATUS")
    print("="*70)
    
    response = requests.get("https://clob.polymarket.com/markets")
    
    if response.status_code != 200:
        print(f"Error: {response.status_code}")
        return
    
    data = response.json()
    markets = data.get('data', [])
    
    print(f"\nTotal markets returned: {len(markets)}")
    
    # Categorize markets
    active_count = 0
    closed_count = 0
    accepting_orders_count = 0
    not_closed_but_not_accepting = []
    
    for market in markets:
        is_active = market.get('active', False)
        is_closed = market.get('closed', False)
        is_accepting = market.get('accepting_orders', False)
        
        if is_active:
            active_count += 1
        if is_closed:
            closed_count += 1
        if is_accepting:
            accepting_orders_count += 1
            
        # Find markets that aren't closed but also not accepting orders
        if not is_closed and not is_accepting:
            not_closed_but_not_accepting.append(market)
    
    print(f"Active markets: {active_count}")
    print(f"Closed markets: {closed_count}")
    print(f"Accepting orders: {accepting_orders_count}")
    print(f"Not closed but not accepting: {len(not_closed_but_not_accepting)}")
    
    # Show some markets that aren't closed
    print("\n" + "="*70)
    print("MARKETS THAT AREN'T CLOSED (but not accepting orders):")
    print("="*70)
    
    for market in not_closed_but_not_accepting[:5]:
        question = market.get('question', 'Unknown')
        end_date = market.get('end_date_iso', 'Unknown')
        active = market.get('active', False)
        
        print(f"\n• {question[:80]}...")
        print(f"  Active: {active}")
        print(f"  Closed: {market.get('closed', False)}")
        print(f"  Accepting orders: {market.get('accepting_orders', False)}")
        print(f"  End date: {end_date}")
        
        # Show token prices
        tokens = market.get('tokens', [])
        for token in tokens[:2]:
            outcome = token.get('outcome', 'Unknown')
            price = token.get('price', 0)
            print(f"  - {outcome}: {price*100:.1f}¢")
    
    # Try alternative endpoints
    print("\n" + "="*70)
    print("TRYING ALTERNATIVE ENDPOINTS:")
    print("="*70)
    
    # Try gamma API
    print("\n1. Trying Gamma API...")
    try:
        gamma_response = requests.get("https://gamma-api.polymarket.com/markets?active=true&limit=5")
        if gamma_response.status_code == 200:
            gamma_data = gamma_response.json()
            if isinstance(gamma_data, list):
                print(f"   Found {len(gamma_data)} markets")
                if gamma_data:
                    first = gamma_data[0]
                    print(f"   Example: {first.get('question', 'Unknown')[:60]}...")
                    print(f"   Accepting orders: {first.get('acceptingOrders', 'Unknown')}")
        else:
            print(f"   Failed: {gamma_response.status_code}")
    except Exception as e:
        print(f"   Error: {e}")
    
    # Check for markets with explicit accepting_orders=true
    print("\n2. Filtering for explicitly accepting orders...")
    accepting_markets = [m for m in markets if m.get('accepting_orders') == True]
    print(f"   Found {len(accepting_markets)} markets explicitly accepting orders")
    
    if accepting_markets:
        print("\n   First accepting market:")
        m = accepting_markets[0]
        print(f"   {m.get('question', 'Unknown')[:80]}...")
        tokens = m.get('tokens', [])
        for token in tokens[:2]:
            print(f"   - {token.get('outcome', 'Unknown')}: {token.get('price', 0)*100:.1f}¢")
    
    # Look for markets that should be active based on dates
    print("\n3. Checking future-dated markets...")
    now = datetime.now(pytz.UTC)
    future_markets = []
    
    for market in markets[:50]:  # Check first 50
        end_date_str = market.get('end_date_iso')
        if end_date_str:
            try:
                end_date = datetime.fromisoformat(end_date_str.replace('Z', '+00:00'))
                if end_date > now:
                    future_markets.append(market)
            except:
                pass
    
    print(f"   Found {len(future_markets)} markets with future end dates")
    if future_markets:
        m = future_markets[0]
        print(f"   Example: {m.get('question', 'Unknown')[:60]}...")
        print(f"   End date: {m.get('end_date_iso', 'Unknown')}")
        print(f"   Accepting orders: {m.get('accepting_orders', False)}")
    
    print("\n" + "="*70)
    print("DIAGNOSIS:")
    print("="*70)
    
    if accepting_orders_count == 0:
        print("""
❌ NO MARKETS ARE CURRENTLY ACCEPTING ORDERS

This could mean:
1. Polymarket is in maintenance mode
2. Markets are between cycles (e.g., waiting for new events)
3. API is returning cached/stale data
4. There's an issue with the API endpoint

SUGGESTIONS:
1. Check Polymarket.com directly to see if you can trade there
2. Try again in a few hours when new markets might open
3. Check their Discord/Twitter for any maintenance announcements
4. Use a specific token ID if you know one from the website
""")
    else:
        print(f"✅ Found {accepting_orders_count} markets accepting orders")
        print("The test script should be able to find these markets")

if __name__ == "__main__":
    debug_active_markets()
