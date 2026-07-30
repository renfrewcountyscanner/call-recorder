package transcription

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient returns an HTTP client configured for a transcription provider endpoint.
// It blocks loopback, link-local, multicast, unspecified and cloud metadata addresses,
// and requires explicit CIDR allowlisting for private addresses.
func HTTPClient(endpoint, allowedCSV string) (*http.Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("invalid transcription endpoint")
	}
	allowed, err := ParseAllowedCIDRs(allowedCSV)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if err := CheckAllowedIP(ip, allowed); err != nil {
			return nil, err
		}
	}
	return &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirects are disabled") },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, e := net.SplitHostPort(address)
				if e != nil {
					return nil, e
				}
				ips, e := net.LookupIP(host)
				if e != nil {
					return nil, e
				}
				if len(ips) == 0 {
					return nil, errors.New("transcription endpoint resolved no addresses")
				}
				var lastErr error
				for _, ip := range ips {
					if err := CheckAllowedIP(ip, allowed); err != nil {
						lastErr = err
						continue
					}
					return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				}
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, errors.New("transcription endpoint resolves to a blocked address")
			},
		},
	}, nil
}

// ParseAllowedCIDRs parses a comma-separated list of CIDRs.
func ParseAllowedCIDRs(allowedCSV string) ([]*net.IPNet, error) {
	allowed := []*net.IPNet{}
	for _, raw := range strings.Split(allowedCSV, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, n, e := net.ParseCIDR(raw)
		if e != nil {
			return nil, fmt.Errorf("invalid transcription endpoint allowlist entry %q", raw)
		}
		allowed = append(allowed, n)
	}
	return allowed, nil
}

// CheckAllowedIP returns nil if the IP is allowed for transcription requests.
func CheckAllowedIP(ip net.IP, allowed []*net.IPNet) error {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.Equal(net.ParseIP("169.254.169.254")) {
		return errors.New("transcription endpoint resolves to a blocked address")
	}
	if ip.IsPrivate() {
		for _, n := range allowed {
			if n.Contains(ip) {
				return nil
			}
		}
		return errors.New("private transcription endpoint is not allowlisted")
	}
	return nil
}

// ValidateAllowedCIDRs validates a comma-separated allowlist string without requiring an endpoint.
func ValidateAllowedCIDRs(allowedCSV string) error {
	_, err := ParseAllowedCIDRs(allowedCSV)
	return err
}
