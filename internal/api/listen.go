package api

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultListenAddress is the loopback-only default for `agenthub serve`.
const DefaultListenAddress = "127.0.0.1:4646"

// ListenAddress is a validated daemon listen address. It records how the
// daemon binds, whether the address is reachable from other machines, and
// which HTTP Host header values are legitimate for this listener.
type ListenAddress struct {
	input    string
	host     string
	port     int
	hostname bool
	ips      []net.IP

	loopback bool
	wildcard bool
	exposed  bool

	allowedIPs   []net.IP
	allowedNames map[string]bool
}

// ResolveListenAddress validates a user supplied host:port listen address.
// It accepts loopback addresses, the unspecified wildcard addresses, IPv4 and
// IPv6 addresses assigned to local interfaces, and hostnames that resolve to
// loopback or local interface addresses. Anything else is an error; the
// resolver never silently falls back to another address.
func ResolveListenAddress(input string) (*ListenAddress, error) {
	return resolveListenAddress(input, lookupHostIPs, localInterfaceIPs, os.Hostname)
}

func resolveListenAddress(input string, lookup func(string) ([]net.IP, error), localAddrs func() ([]net.IP, error), hostname func() (string, error)) (*ListenAddress, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("listen address cannot be empty; use host:port, e.g. %s", DefaultListenAddress)
	}
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address %q: %v (expected host:port; enclose IPv6 addresses in brackets, e.g. [::1]:4646)", trimmed, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("invalid listen address %q: host is empty; specify an explicit address such as 127.0.0.1 or 0.0.0.0", trimmed)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid listen address %q: port %q must be a number between 1 and 65535", trimmed, portText)
	}

	resolved := &ListenAddress{
		input:        trimmed,
		host:         host,
		port:         port,
		allowedNames: map[string]bool{},
	}
	if ip := net.ParseIP(host); ip != nil {
		resolved.ips = []net.IP{ip}
		switch {
		case ip.IsLoopback():
			resolved.loopback = true
		case ip.IsUnspecified():
			resolved.wildcard = true
			resolved.exposed = true
			locals, err := localAddrs()
			if err != nil {
				return nil, fmt.Errorf("cannot enumerate local interface addresses: %w", err)
			}
			resolved.allowedIPs = locals
		default:
			locals, err := localAddrs()
			if err != nil {
				return nil, fmt.Errorf("cannot enumerate local interface addresses: %w", err)
			}
			if !containsIP(locals, ip) {
				return nil, fmt.Errorf("invalid listen address %q: %s is not assigned to a network interface of this machine", trimmed, ip)
			}
			resolved.exposed = true
			resolved.allowedIPs = []net.IP{ip}
		}
	} else {
		ips, err := lookup(host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("invalid listen address %q: cannot resolve host %q to any IP address", trimmed, host)
		}
		locals, err := localAddrs()
		if err != nil {
			return nil, fmt.Errorf("cannot enumerate local interface addresses: %w", err)
		}
		allLoopback := true
		for _, ip := range ips {
			if ip.IsLoopback() {
				continue
			}
			allLoopback = false
			if !containsIP(locals, ip) {
				return nil, fmt.Errorf("invalid listen address %q: host %q resolves to %s, which is not a local interface address", trimmed, host, ip)
			}
		}
		resolved.hostname = true
		resolved.ips = ips
		resolved.loopback = allLoopback
		resolved.exposed = !allLoopback
		resolved.allowedIPs = ips
		resolved.allowedNames[normalizeHostname(host)] = true
	}
	if name, err := hostname(); err == nil && strings.TrimSpace(name) != "" {
		resolved.allowedNames[normalizeHostname(name)] = true
	}
	return resolved, nil
}

// BindAddress returns the address to pass to net.Listen.
func (l *ListenAddress) BindAddress() string {
	return net.JoinHostPort(l.host, strconv.Itoa(l.port))
}

// Port returns the configured TCP port.
func (l *ListenAddress) Port() int {
	return l.port
}

// Loopback reports whether the address is only reachable from this machine.
func (l *ListenAddress) Loopback() bool {
	return l.loopback
}

// Exposed reports whether the address is reachable from other machines on the
// network (a LAN interface address or a wildcard address).
func (l *ListenAddress) Exposed() bool {
	return l.exposed
}

// Endpoint returns the URL local clients (CLI, browser on the same machine)
// should use to reach the daemon. Wildcard listeners are reported through
// loopback because an unspecified address is not a usable client destination.
func (l *ListenAddress) Endpoint() string {
	host := l.host
	if l.wildcard {
		if ip := net.ParseIP(l.host); ip != nil && ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(l.port))
}

// AllowsHost reports whether an HTTP Host header value names this daemon.
// Loopback and localhost are always accepted; interface addresses are only
// accepted when the listener can actually receive traffic for them. This
// blocks DNS-rebinding style requests that target the daemon through an
// attacker controlled name.
func (l *ListenAddress) AllowsHost(hostport string) bool {
	host := strings.TrimSpace(hostport)
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return true
		}
		return containsIP(l.allowedIPs, ip)
	}
	name := normalizeHostname(host)
	if name == "localhost" {
		return true
	}
	return l.allowedNames[name]
}

func normalizeHostname(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func containsIP(list []net.IP, target net.IP) bool {
	for _, ip := range list {
		if ip.Equal(target) {
			return true
		}
	}
	return false
}

func lookupHostIPs(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func localInterfaceIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		switch value := addr.(type) {
		case *net.IPNet:
			ips = append(ips, value.IP)
		case *net.IPAddr:
			ips = append(ips, value.IP)
		}
	}
	return ips, nil
}
