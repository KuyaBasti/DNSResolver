package dns

import (
	"fmt"
	"net/netip"
)

var S = "Fubar"

// RCODE, or response code, is used in DNS for signaling the status of a response,
// This is an enumeration of the different codes possible.
type RCODE uint8

const (
	RCODE_OK RCODE = iota
	RCODE_FMT
	RCODE_SERVFAIL
	RCODE_NXNAME
	RCODE_NOIMPLEMENT
	RCODE_REFUSE
)

var rcodeName = map[RCODE]string{
	RCODE_OK:          "RCODE_OK",
	RCODE_FMT:         "RCODE_FMT",
	RCODE_SERVFAIL:    "RCODE_SERVFAIL",
	RCODE_NXNAME:      "RCODE_NXNAME",
	RCODE_NOIMPLEMENT: "RCODE_NOIMPLEMENT",
	RCODE_REFUSE:      "RCODE_REFUSE",
}

func (rcode RCODE) String() string {
	return rcodeName[rcode]
}

// All DNS data has an RTYPE, a 16b value
// indicating the type of the data.
//
// We will only define RTYPE numbers that this library
// will support, but this is sufficient to do full DNS
// resolution.
type RTYPE uint16

const (
	RTYPE_A     RTYPE = 1
	RTYPE_NS          = 2
	RTYPE_CNAME       = 5
	RTYPE_SOA         = 6
	RTYPE_NULL        = 10
	RTYPE_PTR         = 12
	RTYPE_MX          = 15
	RTYPE_TXT         = 16
	RTYPE_OPT         = 41
	RTYPE_AAAA        = 28
	RTYPE_ANY         = 255
)

var rtypeName = map[RTYPE]string{
	RTYPE_A:     "A",
	RTYPE_NS:    "NS",
	RTYPE_CNAME: "CNAME",
	RTYPE_SOA:   "SOA",
	RTYPE_NULL:  "NULL",
	RTYPE_PTR:   "PTR",
	RTYPE_MX:    "MX",
	RTYPE_TXT:   "TXT",
	RTYPE_OPT:   "OPT",
	RTYPE_AAAA:  "AAAA",
	RTYPE_ANY:   "ANY",
}

func (rtype RTYPE) String() string {
	return rtypeName[rtype]
}

type CLASS int

const (
	IN    CLASS = 1
	CHAOS       = 3
)

var className = map[CLASS]string{
	IN:    "IN",
	CHAOS: "CHAOS",
}

func (c CLASS) String() string {
	return className[c]
}

type DNSQuestion struct {
	QName  string `json:"qname"`
	QType  RTYPE  `json:"qtype"`
	QClass CLASS  `json:"qclass"`
}

func (q DNSQuestion) String() string {
	return fmt.Sprintf("%s %v %v", q.QName, q.QType, q.QClass)
}

type RDATA interface {
	Dummy()
}

type SOA_RECORD struct {
	MName   string `json:"mname"`
	RName   string `json:"rname"`
	Serial  uint32 `json:"serial"`
	Refresh uint32 `json:"refresh"`
	Retry   uint32 `json:"retry"`
	Expire  uint32 `json:"expire"`
}

func (r SOA_RECORD) Dummy() {

}

type NS_RECORD struct {
	NS string `json:"ns"`
}

func (N NS_RECORD) Dummy() {
}

type CNAME_RECORD struct {
	CNAME string `json:"cname"`
}

func (C CNAME_RECORD) Dummy() {
	//TODO implement
}

type A_RECORD struct {
	A netip.Addr `json:"a"`
}

func (a A_RECORD) Dummy() {
}

type AAAA_RECORD struct {
	AAAA netip.Addr `json:"aaaa"`
}

func (A AAAA_RECORD) Dummy() {
}

func (r SOA_RECORD) String() string {
	return fmt.Sprintf("%s %s %v %v %v %v",
		r.MName,
		r.RName,
		r.Serial,
		r.Refresh,
		r.Retry,
		r.Expire)
}

// Go does not have unions, but we can define a
// dummy interface and assign ANYTHING we want as
// rdata and use a type switch or type assertion
type DNSAnswer struct {
	RName  string `json:"rname"`
	RType  RTYPE  `json:"rtype"`
	RClass CLASS  `json:"rclass"`
	RData  RDATA  `json:"rdata"`
}

func (a DNSAnswer) String() string {
	return fmt.Sprintf("%s %v %v %v",
		a.RName,
		a.RType,
		a.RClass,
		a.RData)
}

type DNSHeader struct {
	ID     uint16 `json:"id"`
	Status RCODE  `json:"status"`
}

type DNSMessage struct {
	Header      DNSHeader   `json:"header"`
	Question    DNSQuestion `json:"questions"`
	Answers     []DNSAnswer `json:"answers"`
	Authorities []DNSAnswer `json:"authorities"`
	Additionals []DNSAnswer `json:"additionals"`
}
