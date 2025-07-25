package dns

import (
	"encoding/json"
	"testing"
)

func TestJSON(t *testing.T) {
	q := DNSQuestion{}
	b := []byte(`{"qtype":3,"qclass":4,"qname":"query"}`)
	err := json.Unmarshal(b, &q)
	if err != nil {
		t.Error(err)
	}
	if q.QName != "query" {
		t.Errorf("qname should be 'query', got %s", q.QName)
	}
	if q.QType != 3 {
		t.Errorf("qtype should be 3, got %d", q.QType)
	}
	if q.QClass != 4 {
		t.Errorf("qclass should be 4, got %d", q.QClass)
	}
}

func TestRCODE_String(t *testing.T) {
	tests := []struct {
		name  string
		rcode RCODE
		want  string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rcode.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRTYPE_String(t *testing.T) {
	tests := []struct {
		name  string
		rtype RTYPE
		want  string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rtype.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSOA_RECORD_String(t *testing.T) {
	type fields struct {
		MName   string
		RName   string
		Serial  uint32
		Refresh uint32
		Retry   uint32
		Expire  uint32
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			"Basic",
			fields{"a", "b", 1, 2, 3, 4},
			"a b 1 2 3 4",
		},
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := SOA_RECORD{
				MName:   tt.fields.MName,
				RName:   tt.fields.RName,
				Serial:  tt.fields.Serial,
				Refresh: tt.fields.Refresh,
				Retry:   tt.fields.Retry,
				Expire:  tt.fields.Expire,
			}
			if got := r.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}
