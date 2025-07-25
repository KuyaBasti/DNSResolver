package dns

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func parseAddrNoerror(addr string) netip.Addr {
	tmp, _ := netip.ParseAddr(addr)
	return tmp
}

func TestBasic(t *testing.T) {
	initTestsData(1)
	loadJsonFile("../data/50-lookups.json")
	commConnect = simpleCommManager

	result := QueryLookup("www.mvirtualnet.com.br", RTYPE_A)
	if len(result) != 1 {
		t.Errorf("len(result) = %d; want 1", len(result))
		return
	}
	if result[0].RName != "www.mvirtualnet.com.br" {
		t.Errorf("Wrong query")
	}
	// Realize I had a stupidity and was assuming this should be a string
	// but the proper RData for A is A_RECORD, so this is checking that
	// the A_Record is correct.
	if result[0].RData.(A_RECORD).A != parseAddrNoerror("191.241.53.61") {
		t.Errorf("Wrong query")
	}
}

var currentTest *testing.T = nil

func TestNameHash(t *testing.T) {
	if nameHash("foo.") != nameHash("foo.") {
		t.Errorf("nameHash(foo.) failed")
	}
	if nameHash("foo.") != nameHash("fOo.") {
		t.Errorf("nameHash(fOo.) failed")
	}

	// Technically there should be a 1 in 2^64 chance of this
	// failing.  The hash function isn't a cryptographic hash
	// but it is still a decent one.
	if nameHash("foo.") == nameHash("fo0.") {
		if nameHash("foo.") == nameHash("f0o.") {
			t.Errorf("nameHash(f0o.) collisions.  Should be 1 in 2^64 odds")
		}
	}
}

// We have this function set up to accept a parameter for how
// much fine grained locking we have on the cache.
func initTestsData(n uint) {
	InitCache(n)
	InitServerComm(n)
	commConnect = simpleCommManager
	commLock.Lock()
	defer commLock.Unlock()
	commData = make(map[string]bool)
}

// This is A-records: Given a name whats its IP
var names = make(map[string][]string)

// This is NS-records: Given a domain what are the nameservers
var nameservers = make(map[string][]string)

// This is CNAME records: given a name what is it an alias for?
var cnames = make(map[string]string)

// This is NXNAME records: The name just does not exist
var nxnames = make(map[string]bool)

// This is mapping NS IP to what domains it supports
var ipnameservers = make(map[string][]string)

func loadJsonFile(filename string) {
	f, err := os.OpenFile(filename, os.O_RDONLY, os.ModePerm)
	if err != nil {
		log.Fatalf("open file error: %v", err)
		return
	}
	defer f.Close()

	rd := bufio.NewReader(f)
	line, err := rd.ReadString('\n')
	if err != nil {
		log.Fatalf("read file line error: %v", err)
	}
	err = json.Unmarshal([]byte(line), &names)
	if err != nil {
		log.Fatalf("read file json error: %v", err)
	}

	line, err = rd.ReadString('\n')
	if err != nil {
		log.Fatalf("read file line error: %v", err)
	}
	err = json.Unmarshal([]byte(line), &nameservers)
	if err != nil {
		log.Fatalf("read file json error: %v", err)
	}
	line, err = rd.ReadString('\n')
	if err != nil {
		log.Fatalf("read file line error: %v", err)
	}
	err = json.Unmarshal([]byte(line), &cnames)
	if err != nil {
		log.Fatalf("read file json error: %v", err)
	}

	line, err = rd.ReadString('\n')
	if err != nil {
		log.Fatalf("read file line error: %v", err)
	}
	err = json.Unmarshal([]byte(line), &nxnames)
	if err != nil {
		log.Fatalf("read file json error: %v", err)
	}

	for domain, nameservers := range nameservers {
		_ = domain
		_ = nameservers
		for _, ns := range nameservers {
			_ = ns
			_, ok := cnames[ns]
			if ok {
				count := 0
				for ok {
					ns, _ = cnames[ns]
					_, ok = cnames[ns]
					count++
					if count > 10 {
						fmt.Printf("Apparent CNAME loop, ignoring\n")
						break
					}
				}
			}

			ips, ok := names[ns]
			if ok {
				for _, ip := range ips {
					ipnameservers[ip] = append(ipnameservers[ip], domain)
				}
			}
		}
	}
}

var commLock = sync.Mutex{}
var commData = make(map[string]bool)

// It is only allowed to have a communication for an
// IP on a unique basis...
func simpleCommManager(addr *netip.Addr) *serverCommManager {
	commLock.Lock()
	defer commLock.Unlock()
	_, ok := commData[addr.String()]
	if ok {
		if currentTest != nil {
			currentTest.Errorf("duplicate comm manager")
		} else {
			panic("duplicate comm manager")
		}
	}
	manager := serverCommManager{addr,
		make(chan (*serverDNSRequest))}
	go func() {
		for true {
			request := <-manager.requests
			go get_result(addr.String(), request)
		}
	}()
	return &manager
}

func createAnswer(tld string) *DNSMessage {
	msg := &DNSMessage{
		Header:      DNSHeader{},
		Question:    DNSQuestion{},
		Answers:     make([]DNSAnswer, 0),
		Authorities: make([]DNSAnswer, 0),
		Additionals: make([]DNSAnswer, 0),
	}
	msg.Header.Status = RCODE_OK
	arecords, ok := names[tld]
	if ok {
		for _, arecord := range arecords {
			msg.Answers = append(msg.Answers, DNSAnswer{
				RName:  tld,
				RType:  RTYPE_A,
				RClass: 0,
				RData:  A_RECORD{parseAddrNoerror(arecord)},
			})
		}
	} else {
		cname, ok := cnames[tld]
		if ok {
			msg.Answers = append(msg.Answers, DNSAnswer{
				RName:  tld,
				RType:  RTYPE_CNAME,
				RClass: 0,
				RData:  CNAME_RECORD{cname},
			})

		} else {
			msg.Header.Status = RCODE_NXNAME
		}
	}

	return msg
}

func create_ns(tld string) *DNSMessage {
	ns, ok := nameservers[tld]

	msg := &DNSMessage{
		Header:      DNSHeader{},
		Question:    DNSQuestion{},
		Answers:     make([]DNSAnswer, 0),
		Authorities: make([]DNSAnswer, 0),
		Additionals: make([]DNSAnswer, 0),
	}
	msg.Header.Status = RCODE_OK

	if !ok {
		fmt.Printf("Unable to find NS for TLD %s\n", tld)
		msg.Header.Status = RCODE_SERVFAIL
	}

	for _, server := range ns {
		a := DNSAnswer{
			RName:  tld,
			RType:  RTYPE_NS,
			RClass: 0,
			RData:  NS_RECORD{server},
		}
		msg.Authorities = append(msg.Authorities, a)

		for _, glue := range names[server] {
			g := DNSAnswer{
				RName: server,

				RType:  RTYPE_A,
				RClass: 0,
				RData:  A_RECORD{parseAddrNoerror(glue)},
			}
			msg.Additionals = append(msg.Additionals, g)
		}
	}
	return msg
}

func get_result(addr string, request *serverDNSRequest) {
	domains := ipnameservers[addr]
	query := strings.Split(request.name, ".")
	if domains[0] == "." {
		tld := query[len(query)-1]
		_, ok := nameservers[tld]
		if !ok {
			if len(query) < 2 {
				fmt.Printf("Unable to find domain %s\n", tld)
				return
			}
			tld = query[len(query)-2] + "." + query[len(query)-1]
			_, ok := nameservers[query[len(query)-2]+"."+query[len(query)-1]]
			if !ok {
				fmt.Printf("Unable to find domain %s\n", tld)
				return // Trigger a timeout.
			}
		}
		go func() {
			request.response <- create_ns(tld)
		}()
		return
	}
	for _, domain := range domains {
		if strings.HasSuffix(request.name, domain) {
			cut, _ := strings.CutSuffix(request.name, "."+domain)
			if !strings.Contains(cut, ".") {
				go func() {
					request.response <- createAnswer(request.name)
				}()
				return
			}
			remains := strings.Split(cut, ".")
			current, _ := strings.CutSuffix(cut, "."+remains[len(remains)-1])
			suffix := remains[len(remains)-1] + "." + domain
			for current != "" {
				_, ok := nameservers[suffix]
				if ok {
					go func() {
						request.response <- create_ns(suffix)
					}()
					return
				}
				remains = strings.Split(current, ".")
				tail := remains[len(remains)-1]
				suffix = tail + "." + suffix
				if len(remains) == 1 {
					current = ""
				} else {
					current, _ = strings.CutSuffix(current, "."+tail)
				}
			}
			go func() {
				request.response <- createAnswer(request.name)
			}()
			return
		}
	}
	fmt.Printf("NEED TO IMPLEMENT %v %v\n", domains, request)
}

func TestCommManager(t *testing.T) {
	loadJsonFile("../data/50-lookups.json")
	ip, _ := netip.ParseAddr("199.7.83.42")
	root := simpleCommManager(&ip)
	request := &serverDNSRequest{
		name:     "www.ego.gov.tr",
		qtype:    RTYPE_A,
		response: make(chan *DNSMessage)}
	root.requests <- request
	select {
	case msg := <-request.response:
		fmt.Printf("%v\n", msg)
	case <-time.After(5 * time.Second):
		fmt.Printf("timeout\n")
	}
	ip, _ = netip.ParseAddr("88.255.157.150")
	ns := simpleCommManager(&ip)
	ns.requests <- request
	select {
	case msg := <-request.response:
		fmt.Printf("%v\n", msg)
	case <-time.After(5 * time.Second):
		fmt.Printf("timeout\n")
	}
	ip, _ = netip.ParseAddr("185.7.0.2")
	ns = simpleCommManager(&ip)
	ns.requests <- request
	select {
	case <-request.response:
	case <-time.After(5 * time.Second):
	}
}

func getCommTestInternal(server string, t *testing.T) {
	addr, _ := netip.ParseAddr(server)
	manager := getServerComm(&(addr))
	if manager == nil {
		t.Errorf("Unable to find server")
		return
	}
	if manager.remote.String() != server {
		t.Errorf("Incorrect server")
	}
}

func commManagerTestInternal(cacheSize uint, iterations int, t *testing.T) {
	initTestsData(cacheSize)
	commConnect = simpleCommManager
	done := make(chan bool)
	for server, _ := range ipnameservers {
		for range iterations {
			go func() {
				getCommTestInternal(server, t)
				done <- true
			}()
		}
	}
	for range ipnameservers {
		for range iterations {
			select {
			case <-done:

			case <-time.After(5 * time.Second):
				t.Errorf("timeout")
				return
			}
		}
	}
}

// Here is a set of unit tests for getServerCommManager
// Given we are dealing with race condition bugs we want to really, REALLY
// hammer things so thus the many loops...
func TestGetCommManager(t *testing.T) {
	loadJsonFile("../data/50-lookups.json")
	for range 50 {
		commManagerTestInternal(1, 1, t)
		commManagerTestInternal(1024, 1, t)
		commManagerTestInternal(1, 100, t)
		commManagerTestInternal(1024, 100, t)
	}
	loadJsonFile("../data/bulk.json")
	commManagerTestInternal(1024, 2, t)
}

func TestCacheLookups(t *testing.T) {
	loadJsonFile("../data/bulk.json")
	initTestsData(1024)
	answer := QueryLookup("a.root-servers.net", RTYPE_A)
	if answer == nil || len(answer) == 0 {
		t.Errorf("Unable to find answer")
	}
	if answer[0].RData.(A_RECORD).A.String() != "198.41.0.4" {
		t.Errorf("Wrong answer, expected 198.41.0.4 got %v", answer[0].RData.(A_RECORD).A.String())
	}

}

func TestLotsLookups(t *testing.T) {
	loadJsonFile("../data/bulk.json")
	initTestsData(1024)

	answer := QueryLookup("a.root-servers.net", RTYPE_A)
	if answer == nil || len(answer) == 0 {
		t.Errorf("Unable to find answer")
	}
	if answer[0].RData.(A_RECORD).A.String() != "198.41.0.4" {
		t.Errorf("Wrong answer, expected 198.41.0.4 got %v", answer[0].RData.(A_RECORD).A.String())
	}
	i := 0
	done := make(chan bool)
	for name, _ := range names {
		if i > 4096 {
			break
		}
		i++
		go func() {
			QueryLookup(name, RTYPE_A)
			done <- true
		}()
	}
	i = 0
	for range names {
		if i > 4096 {
			break
		}
		i++
		select {
		case <-time.After(5 * time.Second):
			t.Errorf("timeout")
			return
		case <-done:
		}

	}

}

func TestLotsLookups2(t *testing.T) {
	loadJsonFile("../data/bulk.json")
	initTestsData(32)

	answer := QueryLookup("a.root-servers.net", RTYPE_A)
	if answer == nil || len(answer) == 0 {
		t.Errorf("Unable to find answer")
	}
	if answer[0].RData.(A_RECORD).A.String() != "198.41.0.4" {
		t.Errorf("Wrong answer, expected 198.41.0.4 got %v", answer[0].RData.(A_RECORD).A.String())
	}
	i := 0
	done := make(chan bool)
	for name, _ := range names {
		if i > 4096 {
			break
		}
		i++
		go func() {
			QueryLookup(name, RTYPE_A)
			done <- true
		}()
	}
	i = 0
	for range names {
		if i > 4096 {
			break
		}
		i++
		select {
		case <-time.After(5 * time.Second):
			t.Errorf("timeout")
			return
		case <-done:
		}

	}

}
