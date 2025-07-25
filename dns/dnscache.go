package dns

import (
	"crypto/rand"
	"hash/fnv"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// dnsCacheEntry An actual cache entry, it has both an
// expires time and the rdata itself.
type dnsCacheEntry struct {
	expires time.Time
	data    []RDATA // Changed type signature
}

// dnsCacheUnit This is our basic unit of locking within
// the cache itself.  It consists of both a RWMutex and
// a map between the name/rtype and the cache entry.
// The name should be all lower case when looking/storing
// in this cache.
type dnsCacheUnit struct {
	lock sync.RWMutex
	// An entry itself is a 2-level map, the first being the name
	// (with the trailing '.', in all lower case)
	// and the second being the
	// cache entry itself.
	entries map[string]map[RTYPE]*dnsCacheEntry
}

var dnsCache []*dnsCacheUnit
var seed []byte

// This function needs to be called at the start
// to initialize all the cache entries.  It is
// public because it is part of the setup process
func InitCache(n uint) {
	dnsCache = make([]*dnsCacheUnit, n)
	for i := uint(0); i < n; i++ {
		dnsCache[i] = &dnsCacheUnit{}
	}
	// The error does NOT need to be handled,
	// as rand.Read will ALWAYS fail if it doesn't work
	// with a panic, but just because this is there to
	// suppress a compiler/IDE warning
	_, _ = rand.Read(seed)
	initRoot()
}

func initRoot() {
	rootNS := NS_RECORD{"a.root-servers.net."}
	a, _ := netip.ParseAddr("198.41.0.4")
	rootIP := A_RECORD{a}
	cacheSet(".", RTYPE_NS,
		time.Now().Add(time.Hour*24*365),
		[]RDATA{rootNS})

	cacheSet("a.root-servers.net.",
		RTYPE_A,
		time.Now().Add(time.Hour*24*365),
		[]RDATA{rootIP})

}

// cacheLookup This will look up the entry in the cache for
// the given name and rtype.  If the name doesn't exist, the rtype
// doesn't exist, or the record is expired it should return nil
func cacheLookup(name string, t RTYPE) *dnsCacheEntry {
	// TODO: You need to implement this and make sure this is thread safe.
	// TODO: You need to implement this and make sure this is thread safe.
	name = cleanName(name)
	hunk_index := nameHash(name) % uint32(len(dnsCache))
	key := dnsCache[hunk_index]

	// Using the READER part of the lock
	key.lock.RLock()
	defer key.lock.RUnlock()

	key_entries := key.entries

	// type assertion; cleaner
	domainMAP, isDomain := key_entries[name]
	if isDomain { // entry domain in cache?
		domainCache, inCache := domainMAP[t]
		if inCache { // entry specific RTYPE exist for that domain??
			if domainCache.expires.Before(time.Now()) {
				return nil // entry is expired
			}
			return domainCache // entry exists and in cache and not expired
		}
	}
	return nil
}

// cacheSet This will set a mapping of name/type to RDATA.
// It needs to be thread safe BUT its ok to do additional redundant setting
// if something else at the same time wants to update the data.
// If you want you can add on to the existing data if it makes your life
// easier.
func cacheSet(name string, t RTYPE, expires time.Time, data []RDATA) {
	// TODO: You need to implement this to make sure it is thread safe
	// TODO: You need to implement this to make sure it is thread safe
	// first ocmpute which hunk to use
	name = cleanName(name)
	hunk_index := nameHash(name) % uint32(len(dnsCache))
	key := dnsCache[hunk_index]

	// grab the writer locker
	key.lock.Lock() // this waits until there are no users; using the Reader Lock
	defer key.lock.Unlock()

	key_entries := key.entries

	// discussion
	// do I even have the map that holds all domain names?
	// not asking about specific domain name
	// if key_entries == nil {
	// 	// map(test.com) -> map(A,NS,CNAME?) -> *dnsCacheEntry
	// 	key_entries = make(map[string]map[RTYPE]*dnsCacheEntry)
	// 	// do i even have the specific domain name exist?
	// 	// now we check for specific domain name
	// 	if key_entries[name] == nil {
	// 		// key(test.com) = map(A,NS,CNAME?) -> *dnsCacheEntry
	// 		key_entries[name] = make(map[RTYPE]*dnsCacheEntry)
	// 	}
	// }

	// do I even have the map that holds all domain names?
	// not asking about specific domain name
	if key_entries == nil {
		// map(test.com) -> map(A,NS,CNAME?) -> *dnsCacheEntry
		key_entries = make(map[string]map[RTYPE]*dnsCacheEntry)
	}
	// do i even have the specific domain name exist?
	// now we check for specific domain name
	if key_entries[name] == nil {
		// key(test.com) = map(A,NS,CNAME?) -> *dnsCacheEntry
		key_entries[name] = make(map[RTYPE]*dnsCacheEntry)
	}

	// to fix data an array of pointers
	// slice of pointers
	// new_data := make([]*RDATA, len(data))
	// for i := range data {
	// 	new_data[i] = &data[i]
	// }

	//to set
	//get the index from the nameHash function BELOW
	// create a new dnsCacheEntry object and set its parameters
	// discussion
	newvar := &dnsCacheEntry{
		expires: expires,
		data:    data,
	}
	// discussion
	// throw that new variable into the entries of the cache entry
	key_entries[name][t] = newvar

	key.entries = key_entries
}

// nameHash This is a basic hash function for strings.
// Note this is deliberately nondeterministic between
// runs:  The seed is randomly created.  This is
// to both prevent an attack ("algorithmic complexity attack"
// where an attacker creates a deliberate hot-spot in the cache)
// and to ensure that there is a lot of randomization between
// runs.
func nameHash(name string) uint32 {
	l := strings.ToLower(name)
	h := fnv.New32a()
	_, _ = h.Write([]byte(l))
	_, _ = h.Write(seed)
	return h.Sum32()
}

// serverHash This is the same thing but for server
// addressses using the netip.Addr structure
func serverHash(addr *netip.Addr) uint32 {
	l := addr.String()
	h := fnv.New32a()
	_, _ = h.Write([]byte(l))
	_, _ = h.Write(seed)
	return h.Sum32()
}

// RICO discussion
func cleanName(name string) string {
	// 1.) CLEAN THE STRING
	name = strings.TrimSuffix(name, ".")
	name = strings.ToLower(name)
	// 2.) if empty string "" do name = "."
	if name == "" {
		return "."
	}
	return name
}

// RICO discussion
func bestNS(name string) *dnsCacheEntry {
	// CLEAN IT
	name = cleanName(name)
	// return the best or most specific nameserver you have in the cache
	for {
		entry := cacheLookup(name, RTYPE_NS)
		if entry != nil && len(entry.data) > 0 {
			return entry
		}
		// WE ARE NOT SUPPOSED TO REACH "."
		if name == "." {
			break
		}
		// split it -> rico suuggestion
		nsParts := strings.SplitN(name, ".", 2)
		if len(nsParts) == 2 {
			name = nsParts[1]
		} else {
			name = "."
		}
	}
	// ROOT SERVER IS ALWAYS IN THE CACHE
	return cacheLookup(".", RTYPE_NS)
}

// And this is the heart of the lookup:  Every query executed will be
// in its own coroutine.  It should check the cache for the name and, if present
// & valid, return it.  If not it will need to do iterative lookups
// by first looking up the NS record for the domain (if present) and querying that
//
// If the value is a CNAME it should also follow the CNAME and return that as part of
// the answer.  For now we will only deal with RTYPE_A records
func QueryLookup(name string, t RTYPE) []*DNSAnswer {
	// TODO You need to implement this
	// rico discsuion
	// 1.) CLEAN THE STRING
	// 2.) if the string is empty then return the root server
	name = cleanName(name)

	// we dont care about CNAME
	if t == RTYPE_CNAME {
		return []*DNSAnswer{}
	}

	// apparently go needs you to declare var first rather than just the :=
	// this prevents infinite recursion
	var QueryLookupWithDepth func(string, int) []*DNSAnswer
	QueryLookupWithDepth = func(name string, depth int) []*DNSAnswer {
		// Compute a maximum allowed recursion depth based on how many dots
		// are in the name to prevent infinit recursion
		maxDepth := strings.Count(name, ".")
		if depth > maxDepth {
			return nil
		}
		// 3.) check cache if it knows; if it does then return it
		if entry := cacheLookup(name, t); entry != nil && len(entry.data) > 0 {
			isInCache := make([]*DNSAnswer, len(entry.data))
			for i, adata := range entry.data {
				isInCache[i] = &DNSAnswer{
					RName:  name,
					RType:  t,
					RClass: IN,
					RData:  adata,
				}
			}
			return isInCache
		}
		// 4.) get the best nameserver or most specific from the cache
		nsEntry := bestNS(name) // -> rico discussion
		if nsEntry == nil || len(nsEntry.data) == 0 {
			return nil
		}
		// 5.) get the ip address of that nameserver
		// 5a.) for data in bestNS(name).data
		for _, adata := range nsEntry.data {
			nsRec, isNSRECORD := adata.(NS_RECORD)
			if !isNSRECORD {
				continue
			}
			// - lookup the A-RECORD of that NS entry use adata for this variable using adata.(NS_RECORD).NS
			aRec := cleanName(nsRec.NS)
			adata := cacheLookup(aRec, RTYPE_A)
			// - if that A_record is nil then return nil
			if adata == nil {
				return nil
			}
			//	else check if adata.data != nil AND if the length(adata.data) > 0
			//	if so. then grab the first element and get its netip.Addr maybe a variable named addr := adata.data[0].(A_record).A
			if adata.data != nil && len(adata.data) > 0 {
				addr := adata.data[0].(A_RECORD).A
				// 6.) get the communication manager for the addr from 5.)
				manager := getServerComm(&addr)
				// 7.) make a request using dnsRequest_object(requests)
				req := &serverDNSRequest{
					name:     name,
					qtype:    t,
					response: make(chan *DNSMessage, 1),
				}
				// 8.) make/send a request using servercomm.requests <- request
				manager.requests <- req

				// 9.) wait for response
				var msg *DNSMessage
				select {
				// 9a.) wait for timout
				case <-time.After(3 * time.Second):
					msg = nil
				// 9b.) case response := request.response:
				case msg = <-req.response:
				}
				//	CACHE EVERYTHING
				//	using this cacheSet(answer.Rname, answer.Rtype, time, []RDATA{ANSWER.Rdata} time.now(add 1 year)
				// CACHE ANSWERS
				for _, answers := range msg.Answers {
					cacheSet(answers.RName, answers.RType, time.Now().Add(365*24*time.Hour), []RDATA{answers.RData})
				}
				// CACHE AUTHORITIES
				for _, authorities := range msg.Authorities {
					cacheSet(authorities.RName, authorities.RType, time.Now().Add(365*24*time.Hour), []RDATA{authorities.RData})
				}
				// CACHE ADDITIONALS
				for _, additionals := range msg.Additionals {
					cacheSet(additionals.RName, additionals.RType, time.Now().Add(365*24*time.Hour), []RDATA{additionals.RData})
				}
				// then check if answer in cache and if it does then return it
				if len(msg.Answers) > 0 {
					out := make([]*DNSAnswer, len(msg.Answers))
					for i, answer := range msg.Answers {
						out[i] = &DNSAnswer{
							RName:  answer.RName,
							RType:  answer.RType,
							RClass: IN,
							RData:  answer.RData,
						}
					}
					return out
				}
				// check if we have better more specific nameserver that was cahced
				// if we do have a better NS make a recursive call using QueryLookup(name, t)
				return QueryLookupWithDepth(name, depth+1)
			}
		}
		return nil
	}
	return QueryLookupWithDepth(name, 0)
}

// The protocol for generating a request to a server:
// We send a name and a string for the question, and
// get a response back on the DNSMessage channel.  This
// allows many coroutines to access the underlying server
// communication.

// Critically, however, a server may simply not respond.  In this
// case the process will need to instead have a timeout and go on
// to try another server.
type serverDNSRequest struct {
	name     string
	qtype    RTYPE
	response chan *DNSMessage
}

type serverCommManager struct {
	remote   *netip.Addr
	requests chan *serverDNSRequest
}

type serverCommUnit struct {
	lock sync.RWMutex
	// An entry itself is a 1 level map based
	// on the remote server address
	// Note that we need to be address type not
	// pointer address type due to how things work
	// with maps
	entries map[netip.Addr]*serverCommManager
}

var serverCommCache []*serverCommUnit

// And this inits the cache for server communication.
func InitServerComm(n uint) {
	serverCommCache = make([]*serverCommUnit, n)
	for i := uint(0); i < n; i++ {
		serverCommCache[i] = &serverCommUnit{}
	}
}

func getServerComm(addr *netip.Addr) *serverCommManager {
	// TODO you need to implement this
	// this picks which serverCommUnit hunk holds

	hunk_index := serverHash(addr) % uint32(len(serverCommCache))
	key := serverCommCache[hunk_index]
	key.lock.RLock()
	key_entries := key.entries

	// cache hit
	existing_manager, isCached := key_entries[*addr]
	if isCached {
		key.lock.RUnlock()
		return existing_manager
	}
	// cache miss
	key.lock.RUnlock()
	comm_manager := establishServerComm(addr)

	return comm_manager
}

// This needs to be safe:  It needs to acquire a write lock and first
// make sure that there isn't another write that happened in the meantime.
// If there isn't it should invoke commConnect to get the new server manager
// to be set/returned.
func establishServerComm(addr *netip.Addr) *serverCommManager {
	// TODO you need to implement this.
	hunk_index := serverHash(addr) % uint32(len(serverCommCache))
	key := serverCommCache[hunk_index]

	key.lock.Lock()
	defer key.lock.Unlock()

	// cache miss
	//new_manager := key_entries[*addr]
	new_manager := commConnect(addr)

	return new_manager
}

// commConnect We have our function to create an interface
// to the server manager be a variable rather than a declared
// function to enable testing:  The test infrastructure will use
// a mock version of the function to establish a connection.  If we were
// building a complete server we wolud have this function
// instead do the actual connections.
// This needs to be exposed for now.

// For now we only accept IPv4 (A) record based addresses.
var commConnect func(*netip.Addr) *serverCommManager
