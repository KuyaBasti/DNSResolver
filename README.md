# DNS Resolver

A high-performance, thread-safe DNS resolver implementation in Go featuring recursive resolution, intelligent caching, and comprehensive DNS protocol support. Built from the ground up with modern concurrent programming practices and production-ready reliability.

## 🌟 Features

### Core DNS Functionality
- **Recursive DNS Resolution** - Full recursive lookup starting from root servers
- **Comprehensive Record Support** - A, AAAA, NS, CNAME, SOA, PTR records
- **CNAME Following** - Automatic CNAME chain resolution
- **Root Server Bootstrap** - Built-in root server initialization

### Performance & Scalability
- **Thread-Safe Design** - Concurrent-safe operations using RWMutex
- **Hash-Partitioned Cache** - Reduces lock contention across cache units
- **TTL-Based Expiration** - Automatic cache invalidation
- **Algorithmic Attack Prevention** - Random seeding prevents hash collision attacks

### Reliability
- **Recursion Loop Prevention** - Depth-limited recursive resolution
- **Smart Nameserver Selection** - Intelligent NS record selection algorithm
- **Error Handling** - Comprehensive error responses (NXDOMAIN, SERVFAIL, etc.)
- **Connection Management** - Efficient server communication pooling

## 🏗️ Architecture

### Cache System
```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Cache Unit 0  │    │   Cache Unit 1  │    │   Cache Unit N  │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │   RWMutex   │ │    │ │   RWMutex   │ │    │ │   RWMutex   │ │
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
│                 │    │                 │    │                 │
│ Domain → Record │    │ Domain → Record │    │ Domain → Record │
│      ↓    Type  │    │      ↓    Type  │    │      ↓    Type  │
│   TTL + Data    │    │   TTL + Data    │    │   TTL + Data    │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

### Resolution Flow
```
Query Request
      ↓
┌─────────────┐
│ Cache Check │ ──→ Cache Hit ──→ Return Result
└─────────────┘
      ↓ Cache Miss
┌─────────────┐
│  Find Best  │
│ Nameserver  │
└─────────────┘
      ↓
┌─────────────┐
│   Resolve   │
│ NS Address  │
└─────────────┘
      ↓
┌─────────────┐
│  Query NS   │
│   Server    │
└─────────────┘
      ↓
┌─────────────┐
│ Cache Result│ ──→ Return Result
└─────────────┘
```

## 📚 API Reference

### Core Functions

#### `InitCache(n uint)`
Initializes the DNS cache system with `n` cache units.
```go
dns.InitCache(1024) // Initialize with 1024 cache units
```

#### `QueryLookup(name string, t RTYPE) []*DNSAnswer`
Performs recursive DNS lookup for the given domain name and record type.
```go
results := dns.QueryLookup("google.com", dns.RTYPE_A)
```

### Record Types

| Type | Value | Description |
|------|-------|-------------|
| `RTYPE_A` | 1 | IPv4 address |
| `RTYPE_NS` | 2 | Name server |
| `RTYPE_CNAME` | 5 | Canonical name |
| `RTYPE_SOA` | 6 | Start of authority |
| `RTYPE_PTR` | 12 | Pointer record |
| `RTYPE_AAAA` | 28 | IPv6 address |

### Response Codes

| Code | Meaning | Description |
|------|---------|-------------|
| `RCODE_OK` | Success | Query completed successfully |
| `RCODE_FMT` | Format Error | Query format invalid |
| `RCODE_SERVFAIL` | Server Failure | Server encountered error |
| `RCODE_NXNAME` | Non-existent Domain | Domain doesn't exist |
| `RCODE_NOIMPLEMENT` | Not Implemented | Query type not supported |
| `RCODE_REFUSE` | Query Refused | Server refused query |

## 🧪 Testing

### Run All Tests
```bash
go test ./dns -v
```

### Run Specific Test Suites
```bash
# Basic functionality tests
go test ./dns -run TestBasic -v

# Cache performance tests  
go test ./dns -run TestCacheLookups -v

# Bulk lookup tests
go test ./dns -run TestLotsLookups -v
```

### Test Data
The project includes comprehensive test data:
- **50-lookups.json** - Standard DNS lookups for various domains
- **bulk.json** - Large dataset for performance testing (13MB)

### Performance Benchmarks
```bash
# Run benchmark tests
go test ./dns -bench=. -benchmem
```

## 🔧 Configuration

### Cache Configuration
```go
// Small cache for testing
dns.InitCache(64)

// Production cache (recommended)
dns.InitCache(1024)

// High-performance cache
dns.InitCache(4096)
```

### Server Communication
The resolver uses a configurable server communication manager:
```go
// Initialize server communication pool
dns.InitServerComm(256) // 256 concurrent connections
```

## 📊 Performance Characteristics

### Cache Performance
- **O(1) average lookup time** with hash-based partitioning
- **Concurrent access** supported via RWMutex
- **Memory efficient** with TTL-based cleanup

### Network Performance
- **Connection pooling** for nameserver communications
- **Parallel queries** for multiple record types
- **Smart retry logic** with exponential backoff

### Scalability
- **Linear scaling** with cache unit count
- **Thread-safe** operations throughout
- **Memory usage** scales with active domain count

## 🛡️ Security Features

### Attack Prevention
- **Algorithmic Complexity Attack Prevention** - Random hash seeding
- **Cache Poisoning Protection** - Proper TTL enforcement
- **Recursion Loop Prevention** - Depth-limited resolution
- **Input Validation** - Comprehensive domain name sanitization

### Best Practices
- Thread-safe operations prevent race conditions
- Proper error handling prevents information leakage
- TTL enforcement prevents stale data usage

## 📝 Project Structure

```
DNS-Resolver/
├── dns/
│   ├── dnscache.go       # Core caching implementation
│   ├── dnsmsg.go         # DNS message structures
│   ├── dnslistener.go    # Network listener (placeholder)
│   ├── dnscache_test.go  # Comprehensive test suite
│   └── dnsmsg_test.go    # Message format tests
├── data/
│   ├── 50-lookups.json   # Test lookup data
│   └── bulk.json         # Performance test data
├── main.go               # Entry point
├── go.mod                # Go module definition
└── README.md             # This file
```

## 🔍 Advanced Usage

### Custom Cache Implementation
```go
// Initialize with custom cache size based on expected load
expectedDomains := 10000
cacheUnits := expectedDomains / 10 // 10 domains per unit average
dns.InitCache(uint(cacheUnits))
```

### Batch Lookups
```go
domains := []string{"google.com", "github.com", "stackoverflow.com"}
results := make(map[string][]*dns.DNSAnswer)

for _, domain := range domains {
    results[domain] = dns.QueryLookup(domain, dns.RTYPE_A)
}
```

### Error Handling
```go
results := dns.QueryLookup("nonexistent.domain", dns.RTYPE_A)
if len(results) == 0 {
    fmt.Println("Domain not found or resolution failed")
}
```

## 📈 Performance Tuning

### Cache Optimization
- **Cache Unit Count**: Set to 10-20% of expected concurrent domains
- **Memory vs Speed**: More cache units = less contention but more memory
- **TTL Management**: Tune based on domain change frequency

### Network Optimization
- **Connection Pool Size**: Match to expected concurrent queries
- **Timeout Values**: Balance responsiveness vs reliability
- **Retry Logic**: Configure based on network reliability

## 🐛 Troubleshooting

### Common Issues

**Cache misses despite valid domains**
- Check TTL expiration
- Verify nameserver connectivity
- Ensure proper cache initialization

**High memory usage**
- Reduce cache unit count
- Implement cache cleanup strategies
- Monitor domain turnover rate

**Slow resolution times**
- Increase cache unit count
- Check network connectivity to nameservers
- Consider local DNS server configuration