# DNS Resolver — system design

> How a name becomes an address, one cached fact at a time.
>
> A query enters `QueryLookup` knowing only one thing: the address of a root
> server, planted at startup. Each round of the loop asks the **most specific
> nameserver the cache already knows**, and every record the server volunteers —
> answers, authority NS records, glue addresses — is thrown into a **hash-sharded,
> RWMutex-guarded cache**. Then the loop simply asks itself again: the cache is
> now one delegation deeper, so the next round starts closer to the answer.
> Recursion is bounded by the number of labels in the name; the network is a
> single swappable function variable, replaced in tests by a **mock internet**
> of over a hundred thousand names that answers over channels.

This document is the developer-facing map of the whole system — every component
and how data moves between them. The companion [README](README.md) covers the
per-layer detail, building, and running.

---

## End-to-end flowchart

<p align="center"><img src="docs/system-design-flowchart.svg" alt="DNSResolver end-to-end flowchart. Only the Go tests call QueryLookup; main.go only runs InitCache(1024), and dnslistener.go holds nothing but its package line. QueryLookup cleans the name and returns an empty slice for CNAME queries. Each round then gives up with nil once depth exceeds the dots in the name, returns a fresh cache hit, or asks bestNS for the most specific cached NS entry, whose address must already be cached (otherwise nil). getServerComm read-locks a shard of the manager cache, which is never written, so every round trip takes the write lock and gets a new serverCommManager from the commConnect function variable, which the tests set to the mock simpleCommManager. The request goes over the manager's channel to the mock's get_result, which answers by IP role from the JSON snapshots on the request's response channel, or stays silent if it cannot place the name. No reply within 3 s leaves msg nil and panics. Every returned record is cached for one year under the shard write lock, last write wins; non-empty answers are returned, otherwise the lookup recurses with depth plus one, NXNAME replies included." width="100%"></p>

---

## How to read it: the three ideas that matter

1. **The cache is the resolver's entire world-model.** There is no resolution
   tree, no pending-query table, no per-query state beyond a depth counter.
   `QueryLookup` interleaves two moves — *"what's the most specific NS I
   already know?"* (`bestNS` strips leading labels one at a time, from the
   full name down toward the bootstrapped root) and *one network round trip* — and between them dumps
   every volunteered record into the cache. Referral NS records land where
   `bestNS` looks; glue A records land where the address check looks. The
   recursion doesn't carry knowledge forward, it just re-runs the same
   question against a cache that got smarter — which also means every
   concurrent query sharing a suffix inherits the progress for free.

2. **Sharding makes the cache safe to hammer, cheaply.** Both caches (records
   and server managers) are slices of independent shards, each a plain Go map
   behind its own `RWMutex`; FNV-1a of the key modulo the shard count picks
   one. Readers of a warm shard share the lock; writers exclude only their own
   shard. There is no global lock in either cache (the test mock's `simpleCommManager` does take one, `commLock`, for every manager it builds), and correctness never depends on
   *which* concurrent writer wins — competing `cacheSet`s race one-record
   slices for the same key. The catch is that the surviving NS is a next hop
   only if its glue was also cached: when the last NS listed has no glue,
   the lookup dead-ends. The tests hammer the manager cache at 1 and 1024
   shards and the bulk-lookup stress at 1024 and again at 32: the 1-shard case
   is the adversarial configuration where every operation collides on one lock
   and latent race bugs have nowhere to hide.

3. **The network is a variable.** The only way out of the process is
   `commConnect`, a package-level `func(*netip.Addr) *serverCommManager`.
   Production would dial UDP; the tests assign `simpleCommManager`, which
   fabricates a nameserver per IP out of JSON snapshots and serves it over
   channels — goroutine per server, goroutine per request. Names the mock
   cannot place in any zone it knows are answered with *silence* — the mock's way of simulating a dead server,
   leaving the resolver's 3-second timer as the only defense. (The bulk
   stress tests do sometimes query such a name, and the timeout path then
   panics — see the sharp edges.) The resolver cannot tell a mock from a
   socket, which is the point.

---

## Deep dive 1 — one cold lookup, end to end

`TestBasic` resolves `www.mvirtualnet.com.br` against the 50-lookups snapshot,
starting from a cache that knows only the root — three round trips:

<p align="center"><img src="docs/cold-lookup.svg" alt="DNSResolver, one cold lookup end to end, as a 25-step sequence across the caller goroutine, QueryLookup, the record cache, the comm managers and three mock servers from dnscache_test.go (root, br TLD, authoritative). Before the lookup, initRoot plants the root NS a.root-servers.net at 198.41.0.4. TestBasic calls QueryLookup for www.mvirtualnet.com.br. Hop 1, depth 0: the cache misses, bestNS falls back to the root, getServerComm misses and makes a new manager through commConnect because managers are never stored, and QueryLookup sends the request on manager.requests and waits up to 3 seconds; a timeout would leave the message nil and panic. The root refers to six br nameservers with glue; each record is cached alone for a year, so only f.dns.br survives as the br NS. Hop 2, depth 1: it asks f.dns.br at 200.219.159.10, which refers to mvirtualnet.com.br (ns1 and ns2, glue 191.241.52.28 and .29); only ns2 is kept. Hop 3, depth 2: it asks ns2 at 191.241.52.29, which answers A 191.241.53.61; the answer is cached for a year and returned to the caller as a DNSAnswer with class IN." width="100%"></p>

Things worth noticing:

- **The referral shape is load-bearing.** The mock's `create_ns` puts NS
  records in *Authorities* and glue addresses in *Additionals* — the same
  split real DNS uses — and the resolver's cache-everything step is what
  converts that shape into progress. Drop the glue from the cache and the
  next round's address lookup (step 12 above; step 2 of the README's
  resolution loop) dead-ends.
- **The depth bound is the dot count.** `www.mvirtualnet.com.br` has three
  dots, so at most four rounds (depths 0–3) can run — matching the deepest
  possible delegation chain for a name of that shape; this trace used three
  of them. A referral loop between misbehaving servers burns depth each
  round and terminates with `nil`.
- **A second lookup of anything under `.br` now starts at least one level
  deeper** — the `br` and `mvirtualnet.com.br` delegations are cached
  process-wide, not per query.

## Deep dive 2 — anatomy of a shard

<p align="center"><img src="docs/shard-anatomy.svg" alt="DNSResolver, anatomy of a shard. Picking a shard: the query name www.Example.COM. goes through cleanName, which lowercases it and cuts one trailing dot, giving the key www.example.com; nameHash takes a 32-bit FNV-1a hash of the key and then of the seed, which is a nil slice and adds nothing, giving 0x88469fcb; modulo n = 1024 that is shard 971, the same on every run. The record cache dnsCache is a slice of n *dnsCacheUnit made once by InitCache(n). Each name is hashed on its own, so the name and its parent zones are placed independently; at n = 1024 they land in three shards: com (0xf18dd2de) in dnsCache[734], example.com (0x431ceb26) in dnsCache[806] and www.example.com (0x88469fcb) in dnsCache[971]. Every shard has its own lock, a sync.RWMutex, and its own entries map of type map[string]map[RTYPE]*dnsCacheEntry: com maps through RTYPE_NS to a *dnsCacheEntry, example.com through RTYPE_NS, www.example.com through RTYPE_A. Inside dnsCache[971]: cacheLookup takes RLock so readers share the shard, cacheSet takes Lock so a writer excludes readers and other writers, and the lock guards this shard only. The entry has expires time.Time, stamped now plus 365 days by every caller, after which cacheLookup returns nil but the entry is kept, and data []RDATA, here one A_RECORD because every call site passes one record, and each cacheSet replaces the whole entry. With n = 1 all three keys share dnsCache[0]; with n = 32, com, example.com and www.example.com are in 30, 6 and 11. A shard holds every name whose hash lands on it; one example key per shard is drawn. A shard's entries map is nil until its first cacheSet; after InitCache(1024) only shards 241 and 978 have one, from the root bootstrap." width="100%"></p>

- **Keys are canonical**: `cleanName` lowercases and strips the trailing dot
  (`""` becomes `"."`, the root), and both `cacheLookup` and `cacheSet` clean
  before hashing — so `www.Example.COM.` and `www.example.com` are one entry.
- **Expiry is lazy**: `cacheLookup` returns `nil` for a stale entry but never
  deletes it; there is no eviction of any kind. With everything stamped
  one year out, the cache effectively never shrinks for a process lifetime — entries are only added or overwritten.
- **The maps are born lazily**: a shard starts with a `nil` map; the first
  `cacheSet` on it allocates both levels under the write lock.
- **The hash seed is inert**: FNV-1a is *supposed* to be salted with
  `crypto/rand` bytes per run to prevent adversarial shard-hotspot attacks,
  but the seed slice is nil, so `rand.Read` fills zero bytes and placement is
  deterministic. The defense is one `make([]byte, 16)` away.

The manager cache (`serverCommCache`) mirrors this layout — shards, RWMutex,
one-level `map[netip.Addr]*serverCommManager` — but its write path is
unfinished: `establishServerComm` locks, calls `commConnect`, and returns
without either re-checking or storing, so every miss creates a fresh manager
and goroutine.
The harness's duplicate-manager detector would catch this, except it never
records managers either. Two unwired safety nets, canceling out.

---

## Component inventory

| Component | Layer | Provenance | Where |
|---|---|---|---|
| Message model — enums, `RDATA` union, `DNSMessage` | Types | course scaffolding | [dnsmsg.go](dns/dnsmsg.go) |
| Cache shards, hashing, bootstrap (`InitCache`, `initRoot`) | Cache | course scaffolding | [dnscache.go](dns/dnscache.go) |
| `cacheLookup` / `cacheSet` / `cleanName` / `bestNS` | Cache | ✅ implemented here | [dnscache.go](dns/dnscache.go) |
| `QueryLookup` — the resolution loop | Resolver | ✅ implemented here | [dnscache.go](dns/dnscache.go) |
| `getServerComm` / `establishServerComm` | Comm | ✅ implemented (store unfinished) | [dnscache.go](dns/dnscache.go) |
| `commConnect` seam | Comm | course scaffolding | [dnscache.go](dns/dnscache.go) |
| Mock internet — managers, `get_result`, referral builders | Test harness | course scaffolding | [dnscache_test.go](dns/dnscache_test.go) |
| DNS snapshots (50-lookups, bulk) | Test data | course scaffolding | [data/](data/) |
| Network listener | — | ⬜ placeholder only | [dnslistener.go](dns/dnslistener.go) |
| CLI driver | — | ⬜ stub (`InitCache` and exit) | [main.go](main.go) |

---

## The numbers that matter

| Value | What it is |
|---|---|
| 2 | levels in the record cache map: name → RTYPE → entry |
| 32-bit FNV-1a | shard hash for both caches; shard = hash % n |
| 3 s | per-round-trip server timeout in `QueryLookup` |
| 1 | buffer slot on each response channel — a late reply never blocks the mock |
| 1 year | TTL stamped on the bootstrap and on every cached record |
| 198.41.0.4 | `a.root-servers.net` — the single bootstrapped root, all cold lookups start here |
| dots in the name | recursion depth bound (`depth > strings.Count(name, ".")` → give up) |
| 1 / 32 / 1024 | shard counts exercised by the tests (1 = maximum-contention adversarial case) |
| 4,097 | concurrent lookups per stress test (the `i > 4096` guard overshoots by one) |
| 200 + 1 | manager-cache hammer rounds: 50 × four shard/iteration combos, then one bulk round |
| 102,913 / 84,879 / 24,914 / 17,409 | A names / zones / CNAMEs / NXDOMAINs in bulk.json (~14 MB) |
| 0 | real packets sent — the network below `commConnect` is entirely mocked |

---

## Verification workflow

| Stage | Test | What it proves |
|---|---|---|
| 1 | `TestJSON`, `TestSOA_RECORD_String`, `TestNameHash` | a `DNSQuestion` decodes from JSON, `SOA_RECORD` formats correctly, hashing is stable and case-insensitive |
| 2 | `TestCommManager` | the mock hierarchy responds at every level when walked by hand (root referral, authoritative answer, TLD referral). The first two replies are printed for inspection; the third is received and discarded. No assertions |
| 3 | `TestBasic` | one cold recursive lookup end to end — `www.mvirtualnet.com.br` → `191.241.53.61` |
| 4 | `TestGetCommManager` | manager cache hammered 50 rounds × {1, 1024 shards} × {1, 100 goroutines per server}, then a bulk round — every call must return a manager for the requested address, with no 5 s stall; the duplicate-manager check never fires (see above), so the missing store goes unnoticed |
| 5 | `TestCacheLookups` | bootstrapped records are served straight from the cache |
| 6 | `TestLotsLookups` / `TestLotsLookups2` | 4,097 concurrent lookups against the 85,000-zone bulk snapshot, at 1024 and again at 32 shards; fails if 5 s pass without any lookup completing |

The full suite runs in ~19 s (`go test ./dns -v`), dominated by the
`TestGetCommManager` hammer, but it is not reliably green: `TestLotsLookups`
and `TestLotsLookups2` intermittently query a name the mock leaves
unanswered and panic on the timeout path (see the sharp edges). The race
detector passes on the small lookup tests
(`go test ./dns -race -run 'TestBasic|TestCacheLookups'`).

---

## Design trade-offs & sharp edges

- **Cache-as-world-model over explicit resolution state** — radically simple
  and naturally shared across concurrent queries, but it means resolution
  correctness *depends* on caching policy: the loop only advances because
  referrals and glue are cached, and the cached address lookup after `bestNS`
  (step 2 of the README's resolution loop; steps 12 and 20 in the cold-lookup
  diagram) refuses to recurse — an NS without cached glue is a dead end rather
  than a sub-lookup.
- **Depth-by-dot-count over loop detection** — no cycle bookkeeping at all,
  at the cost of giving up early on delegation chains longer than the name is
  deep (rare in practice, impossible in the test data).
- **Last-writer-wins caching over entry merging** — `cacheSet` replaces the
  RDATA slice wholesale, and `QueryLookup` caches each referral record as its
  own one-element slice, so even a single referral's NS records (six for `br` in deep dive 1) overwrite
  one another and the zone entry keeps only the last. The surviving NS is
  usable only if its glue was cached; zones whose last-listed NS has no glue
  dead-end on a cold cache (29 of the 78 zones in `50-lookups.json`).
- **A 1-year uniform TTL over real TTL plumbing** — `DNSAnswer` has no TTL
  field, so the resolver invents one. Combined with lazy expiry and no
  eviction, the cache only ever grows; fine for a test-driven homework
  process, unbounded for a daemon.
- **The unfinished manager store** — every round trip creates a fresh
  manager (goroutine included), so the sharded manager cache currently provides
  sharding without caching. Correct behavior, but an unbounded leak — one
  manager and one permanent goroutine per round trip.
- **The timeout path is a trap** — a server that stays silent for 3 seconds
  leads straight to a nil-pointer panic on `msg.Answers`. The mock simulates
  dead servers, and the bulk stress tests intermittently hit one and crash
  here, which makes them flaky.
- **Determinism where randomness was intended** — the unseeded hash (see deep
  dive 2) removes the anti-hotspot defense but makes shard placement
  reproducible, which is arguably a debugging feature in a homework setting.

---

## Provenance

ECS 158 (Parallel Architectures, UC Davis) homework — module `ECS-158-HW1`.
Course scaffolding provided the type definitions, function signatures and doc
comments, the mock-internet test harness, and the JSON snapshots; the resolver
logic implemented on top of it is the cache (`cacheLookup`, `cacheSet`,
`cleanName`, `bestNS`), the resolution loop (`QueryLookup`), and the server-comm
lookup path (`getServerComm`, `establishServerComm`) in
[dnscache.go](dns/dnscache.go). The `// RICO discussion` comments mark ideas
worked out in discussion section.
