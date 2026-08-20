# DNS Resolver

A concurrent, recursive DNS resolver in Go. `QueryLookup` performs **iterative resolution from the root**: it walks the delegation hierarchy one nameserver at a time, learning as it goes, backed by a **hash-sharded, TTL-aware cache** (per-shard `RWMutex`, two-level `name → RTYPE → entry` maps) and a **channel-based server-communication layer** where every remote nameserver gets its own request channel. The network itself is a **pluggable function variable** — the test suite swaps in a mock internet built from JSON snapshots of real DNS data (102,913 names in the bulk set) and hammers the resolver with thousands of concurrent goroutine lookups.

The interesting part isn't the DNS arcana — it's the shape of the resolver: **resolution is implemented as a cache that learns**. There is no resolution tree or pending-query state machine. `QueryLookup` alternates between "what's the most specific nameserver I already know for this name?" and one network round-trip, dumps every record the server volunteers (answers, authorities, glue) back into the cache, and then re-asks itself the same question against a now-smarter cache. Progress lives in the cache, not the call stack; the recursion is just a bounded retry.

---

## Table of Contents

1. [How a Lookup Works](#how-a-lookup-works)
2. [Repository Map](#repository-map)
3. [The Message Model](#the-message-model)
4. [The Cache — Two-Level Maps Behind Sharded Locks](#the-cache--two-level-maps-behind-sharded-locks)
5. [The Resolution Loop](#the-resolution-loop)
6. [The Server-Communication Layer](#the-server-communication-layer)
7. [The Mock Internet](#the-mock-internet)
8. [Concurrency Model](#concurrency-model)
9. [Build & Test](#build--test)
10. [Known Limitations & Sharp Edges](#known-limitations--sharp-edges)
11. [Provenance](#provenance)

---

## How a Lookup Works

```mermaid
flowchart TD
    Q["QueryLookup(name, RTYPE_A)"] --> CLEAN["cleanName — lowercase,<br/>strip trailing dot, '' becomes '.'"]
    CLEAN --> HIT{"cacheLookup(name, type)<br/>fresh entry?"}
    HIT -- "hit" --> RET["return answers from cache"]
    HIT -- "miss" --> BEST["bestNS — strip leading labels<br/>www.example.com → example.com → com → ."]
    BEST --> GLUE{"A record for that<br/>nameserver in cache?"}
    GLUE -- "no" --> NIL["return nil — dead end"]
    GLUE -- "yes" --> MGR["getServerComm(addr) —<br/>manager for that server IP"]
    MGR --> SEND["send serverDNSRequest<br/>on the manager's channel"]
    SEND --> WAIT{"response<br/>within 3 s?"}
    WAIT -- "timeout" --> PANIC["msg = nil → nil-pointer panic<br/>(see sharp edges)"]
    WAIT -- "response" --> CACHE["cacheSet everything:<br/>answers + authorities + additionals,<br/>1-year TTL"]
    CACHE --> ANS{"msg.Answers<br/>non-empty?"}
    ANS -- "yes" --> RET2["return the answers"]
    ANS -- "no — referral only" --> DEPTH{"depth ≤ dots<br/>in name?"}
    DEPTH -- "yes — recurse" --> HIT
    DEPTH -- "no" --> NIL2["return nil"]
```

One pass through this loop takes exactly one network step. For `www.mvirtualnet.com.br` starting cold, the cache knows only the bootstrapped root, so the walk is: root server → learn the `br` delegation (+ glue) → ask a `br` server → learn the authoritative `mvirtualnet.com.br` delegation (+ glue) → ask it → get the `A` record — three round trips, answered at depth 2 of an allowed 3. Each round trip enriches the cache, and `bestNS` automatically starts deeper on the next pass — including for *every other concurrent query* that shares a suffix.

## Repository Map

```text
DNSResolver/
├── README.md               # you are here
├── SYSTEM-DESIGN.md        # the architecture-level view
├── go.mod                  # module ECS-158-HW1, Go 1.24
├── main.go                 # stub driver — InitCache(1024); the test suite is the real driver
├── data/
│   ├── 50-lookups.json     # small mock-internet snapshot: 227 A names, 78 zones
│   └── bulk.json           # ~14 MB snapshot: 102,913 A names, 84,879 zones,
│                           #   24,914 CNAMEs, 17,409 NXDOMAIN names
└── dns/
    ├── dnsmsg.go           # message model: RTYPE/RCODE/CLASS enums, RDATA union, DNSMessage
    ├── dnscache.go         # the resolver: sharded cache, FNV hashing, bestNS,
    │                       #   QueryLookup, server-comm manager pool
    ├── dnslistener.go      # empty placeholder — a real network listener never landed
    ├── dnscache_test.go    # the mock internet + resolution, hammer, and stress tests
    └── dnsmsg_test.go      # JSON decode + String() tests
```

## The Message Model

[dnsmsg.go](dns/dnsmsg.go) defines DNS messages as plain Go structs — there is **no wire-format encoding or parsing** anywhere in the repo. A `DNSMessage` is a header (just an ID and an `RCODE` status), one question, and the three classic record sections:

```go
type DNSMessage struct {
    Header      DNSHeader
    Question    DNSQuestion
    Answers     []DNSAnswer
    Authorities []DNSAnswer
    Additionals []DNSAnswer
}
```

Record data uses the classic Go union idiom: `RDATA` is a marker interface with a single `Dummy()` method, and `A_RECORD`, `AAAA_RECORD`, `NS_RECORD`, `CNAME_RECORD`, and `SOA_RECORD` all implement it. Consumers type-assert (`rdata.(A_RECORD).A`) to get the concrete data back out. Every struct carries JSON tags, which is how the test data flows in.

The `RTYPE` enum covers A, NS, CNAME, SOA, NULL, PTR, MX, TXT, OPT, AAAA, and ANY — but only **A, NS, and CNAME records actually flow through the resolution path**. The rest are type definitions waiting for a use.

## The Cache — Two-Level Maps Behind Sharded Locks

```mermaid
flowchart LR
    NAME["query name<br/>'www.Example.COM.'"] -- "cleanName" --> KEY["'www.example.com'<br/>lowercase, no trailing dot"]
    KEY -- "FNV-1a hash<br/>% shard count" --> SHARD["dnsCache[i]<br/>one RWMutex per shard"]
    SHARD --> L1["entries[name]"]
    L1 --> L2["[RTYPE] → dnsCacheEntry"]
    L2 --> E["expires time.Time<br/>data []RDATA"]
```

The cache ([dnscache.go](dns/dnscache.go)) is a slice of `n` shards, allocated once by `InitCache(n)`. Each shard owns an `RWMutex` and a two-level map: name → record type → entry. Shard selection hashes the lowercased name with **32-bit FNV-1a** and takes it modulo the shard count, so unrelated names land on unrelated locks and readers of the same shard proceed in parallel.

- **`cacheLookup`** takes the shard's *read* lock, walks the two map levels, and treats an expired entry as a miss (`expires.Before(now)` → `nil`). Expiry is **lazy**: nothing is ever deleted, entries are just ignored once stale.
- **`cacheSet`** takes the *write* lock, lazily creates both map levels on first touch, and overwrites whatever was there — last writer wins, which is explicitly fine here: redundant concurrent sets of the same answer are harmless.
- **`initRoot`** bootstraps the hierarchy: `InitCache` plants `.` → `NS a.root-servers.net` and `a.root-servers.net` → `A 198.41.0.4` with a one-year TTL. That single hardcoded root is the seed every cold lookup grows from.
- **`bestNS`** is the delegation walker: starting from the full name, it strips the leftmost label until it finds a cached NS record, bottoming out at `.` — which always exists thanks to the bootstrap.

The hash is *intended* to be seeded with random bytes per run (an anti-hotspot / algorithmic-complexity defense), but the seeding is inert — see [sharp edges](#known-limitations--sharp-edges).

## The Resolution Loop

`QueryLookup(name, type)` is the public entry point, designed to be called from many goroutines at once. Internally it defines a recursive closure `QueryLookupWithDepth` with a **depth bound equal to the number of dots in the cleaned name** — a `www.example.com` lookup gets at most 3 rounds (depth 0, 1, 2 pass the `depth > 2` guard; each round is one server query), which is exactly the number of delegation steps a name that deep can need, and makes infinite delegation loops structurally impossible.

Each round:

1. **Cache check** — a fresh entry short-circuits everything; hits are wrapped in `DNSAnswer`s and returned.
2. **Find a server** — `bestNS(name)` yields the most specific cached NS set; the loop takes its first `NS_RECORD` and looks the server's **A record up in the cache** (it must have arrived earlier as glue). No cached address → `return nil`, a hard dead end — even if a sibling NS had glue.
3. **One round trip** — build a `serverDNSRequest` with a 1-buffered response channel, send it to the server's manager, and `select` on the response vs. a 3-second timer.
4. **Cache everything** — answers, authorities, *and* additionals all go in via `cacheSet`, each stamped `now + 1 year` (the mock messages carry no TTLs). This is the step that makes the next round smarter: a referral's NS records and glue land in exactly the places `bestNS` and step 2 look.
5. **Answer or recurse** — a non-empty `Answers` section is returned as-is; an empty one (a pure referral) triggers `QueryLookupWithDepth(name, depth+1)`, which re-runs the loop against the improved cache.

Two deliberate scope cuts: direct `CNAME` queries return an empty slice immediately, and a `CNAME` that arrives as the *answer* to an `A` query is cached and returned but **not chased** to its target address.

## The Server-Communication Layer

Talking to a nameserver goes through a `serverCommManager` — a tiny struct pairing the remote `netip.Addr` with a `chan *serverDNSRequest`. The request/response protocol is pure channels: the querier sends `{name, qtype, response chan}` and waits on its own response channel, so **one manager can serve unbounded concurrent queriers** with no locking on the hot path.

Managers live in `serverCommCache`, a second sharded structure (`InitServerComm(n)`) mirroring the record cache: FNV-1a over the address string picks a shard, each shard has an `RWMutex` and a `map[netip.Addr]*serverCommManager`. `getServerComm` does an optimistic read-locked lookup and falls back to `establishServerComm` on a miss, which is supposed to double-check under the write lock and store the new manager — but currently does neither (see [sharp edges](#known-limitations--sharp-edges)).

The actual "connect" is the package variable `commConnect func(*netip.Addr) *serverCommManager`. Production code would dial UDP here; the tests assign a mock instead. That one variable is the entire seam between the resolver and the network.

## The Mock Internet

The test file [dnscache_test.go](dns/dnscache_test.go) implements a simulated DNS hierarchy that behaves like the real one:

- **Snapshots** — each file in [data/](data/) is four lines of JSON: `names` (name → IPs), `nameservers` (zone → NS hostnames), `cnames` (alias → target), and `nxnames`. `loadJsonFile` inverts them into `ipnameservers` (server IP → zones it serves), following CNAME chains while doing so and bailing out with an "Apparent CNAME loop" warning once a chain exceeds the 10-hop guard (which, off by one, actually allows 11).
- **Fake servers** — the mock `commConnect` (`simpleCommManager`) spawns one goroutine per manager that dispatches each incoming request to `get_result` in yet another goroutine.
- **Role by IP** — `get_result` decides what kind of server it is by looking up its own address in `ipnameservers`. A root server (serving `.`) answers with a TLD delegation: NS records in *Authorities*, glue A records in *Additionals* — exactly the referral shape the resolver's cache-everything step depends on. A mid-hierarchy server walks the queried name down to the deepest zone it knows and either delegates again or answers with A records, a CNAME, or an `NXNAME` status.
- **Dead servers** — a name the mock can't place gets **no response at all** (`return // Trigger a timeout.`), simulating an unresponsive nameserver. No test actually queries such a name, which is fortunate — the resolver's timeout path panics (see [sharp edges](#known-limitations--sharp-edges)).

This is what lets the stress tests fire 4,097 concurrent lookups against an 85,000-zone hierarchy without a single real packet.

## Concurrency Model

- **Goroutine-per-query** — `QueryLookup` is self-contained; the stress tests run thousands simultaneously and the design assumes it.
- **Sharded locking, both caches** — record cache and manager cache each split their keyspace across independent `RWMutex`-guarded shards. Reads (the overwhelmingly common case once warm) take shared locks; writes lock only their own shard. The manager-cache hammer runs at 1 and 1024 shards, and the bulk-lookup stress at 1024 and again at 32 — collectively covering the 1-shard adversarial case where every operation serializes on one lock.
- **Channels for the network** — request/response is a send plus a `select` with a timeout; the response channel's single buffer slot lets a late-arriving mock reply complete without a receiver.
- **Benign races by design** — `cacheSet` tolerates concurrent redundant writes (last one wins); correctness never depends on which duplicate lands.

## Build & Test

Requires Go 1.24+. No dependencies outside the standard library.

```bash
go build ./...
```

The tests load `../data/*.json` relative to the `dns/` package directory — which is where `go test` runs the test binary — so this works from anywhere in the module:

```bash
go test ./dns -v
```

Useful selections:

```bash
go test ./dns -run TestBasic -v          # one cold recursive lookup, end to end
go test ./dns -run TestGetCommManager -v # 200 rounds of manager-cache hammering
go test ./dns -run TestLotsLookups -v    # 4,097 concurrent lookups vs bulk.json
go test ./dns -race -run TestBasic       # the race detector is happy with the small tests
```

`main.go` is a stub (it only initializes the cache) — the test suite is the real driver.

## Known Limitations & Sharp Edges

Honest notes — several are scope decisions, a few are latent bugs the current tests can't see:

- **The hash seed is inert.** `seed` is declared as a nil slice and `rand.Read(seed)` fills exactly `len(seed) = 0` bytes, so the "randomized per run" anti-hotspot defense described in the comments never happens — hashing is plain deterministic FNV-1a. The fix is one line (`seed = make([]byte, 16)` before the read), but as written, shard placement is identical every run.
- **The manager cache never stores managers.** `establishServerComm` takes the write lock but skips both the double-check and the `entries[*addr] = manager` store, so *every* `getServerComm` call misses and dials a fresh manager (plus its goroutine). The test harness has a duplicate-manager detector meant to catch exactly this — but it's also inert, because `simpleCommManager` checks `commData` without ever inserting into it. Two safety nets, both unwired, canceling each other out.
- **The timeout path panics.** On a 3-second timeout `msg` stays `nil` and the very next line ranges over `msg.Answers` — a nil-pointer dereference. The tests only query names the mock can answer, so the "dead server" path is never actually walked to the crash.
- **Only the first usable NS is tried.** If the first `NS_RECORD`'s address isn't cached, the lookup returns `nil` even if a sibling NS had glue; and after one server round trip the function always returns rather than trying the next server. The "on timeout, move to the next server" behavior the comments describe doesn't exist yet.
- **CNAMEs are recorded, not resolved.** An `A` query answered with a CNAME returns the `CNAME_RECORD` itself; nothing looks up the target. Direct CNAME queries short-circuit to an empty result by design.
- **Every record lives for a year.** The mock messages carry no TTLs, so the resolver stamps `now + 365 days` on everything it caches, and expiry is lazy with no eviction — the cache grows monotonically. Real TTL plumbing would need TTL fields on `DNSAnswer`.
- **[dnslistener.go](dns/dnslistener.go) is one line** (`package dns`) — there is no wire protocol, no UDP socket, no listener. The resolver's "network" is whatever `commConnect` is assigned.
- **Leftovers** — `var S = "Fubar"` in [dnsmsg.go](dns/dnsmsg.go), a `currentTest` variable that is never set, and `loadJsonFile` *appends* into shared maps, so loading `bulk.json` after `50-lookups.json` accumulates both snapshots rather than replacing one with the other.

## Provenance

This is coursework from **ECS 158 (Parallel Architectures), UC Davis** — the module name `ECS-158-HW1` gives it away. The type definitions, function signatures, doc comments, and the entire test harness (including the mock internet and its JSON snapshots) were provided as scaffolding; the implemented work is the resolver logic itself: `cacheLookup`, `cacheSet`, `cleanName`, `bestNS`, `QueryLookup`, `getServerComm`, and `establishServerComm` in [dnscache.go](dns/dnscache.go). The `// RICO discussion` comments mark ideas worked out in discussion section.

See [SYSTEM-DESIGN.md](SYSTEM-DESIGN.md) for the architecture-level view: the full data-flow diagram, the ideas behind the design, and the numbers that matter.
