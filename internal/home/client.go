package home

import (
	"encoding"
	"fmt"

	"github.com/AdguardTeam/dnsproxy/proxy"
)

// Client contains information about persistent clients.
type Client struct {
	// upstreamConfig is the custom upstream config for this client.  If
	// it's nil, it has not been initialized yet.  If it's non-nil and
	// empty, there are no valid upstreams.  If it's non-nil and non-empty,
	// these upstream must be used.
	upstreamConfig *proxy.UpstreamConfig

	Name string

	IDs             []string
	Tags            []string
	BlockedServices []string
	Upstreams       []string

	UseOwnSettings        bool
	FilteringEnabled      bool
	SafeSearchEnabled     bool
	SafeBrowsingEnabled   bool
	ParentalEnabled       bool
	UseOwnBlockedServices bool
}

// closeUpstreams closes the client-specific upstream config of c if any.
func (c *Client) closeUpstreams() (err error) {
	if c.upstreamConfig != nil {
		err = c.upstreamConfig.Close()
		if err != nil {
			return fmt.Errorf("closing upstreams of client %q: %w", c.Name, err)
		}
	}

	return nil
}

// clientSource represents the source from which the information about the
// client has been obtained.
type clientSource uint

// Clients information sources.  The order determines the priority.
const (
	ClientSourceNone clientSource = iota
	ClientSourceWHOIS
	ClientSourceARP
	ClientSourceRDNS
	ClientSourceDHCP
	ClientSourceHostsFile
	ClientSourcePersistent
)

// type check
var _ fmt.Stringer = clientSource(0)

// String returns a human-readable name of cs.
func (cs clientSource) String() (s string) {
	switch cs {
	case ClientSourceWHOIS:
		return "WHOIS"
	case ClientSourceARP:
		return "ARP"
	case ClientSourceRDNS:
		return "rDNS"
	case ClientSourceDHCP:
		return "DHCP"
	case ClientSourceHostsFile:
		return "etc/hosts"
	default:
		return ""
	}
}

// type check
var _ encoding.TextMarshaler = clientSource(0)

// MarshalText implements encoding.TextMarshaler for the clientSource.
func (cs clientSource) MarshalText() (text []byte, err error) {
	return []byte(cs.String()), nil
}

// RuntimeClient is a client information about which has been obtained using the
// source described in the Source field.
type RuntimeClient struct {
	WHOISInfo *RuntimeClientWHOISInfo
	Host      string
	Source    clientSource
}

// RuntimeClientWHOISInfo is the filtered WHOIS data for a runtime client.
type RuntimeClientWHOISInfo struct {
	City    string `json:"city,omitempty"`
	Country string `json:"country,omitempty"`
	Orgname string `json:"orgname,omitempty"`
}
