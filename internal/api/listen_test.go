package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disksing/project-incubator/agenthub/internal/session"
)

var (
	testLANIPv4 = net.ParseIP("192.168.1.10")
	testLANIPv6 = net.ParseIP("fd00::10")
)

func fakeLookup(mapping map[string][]net.IP) func(string) ([]net.IP, error) {
	return func(host string) ([]net.IP, error) {
		if ips, ok := mapping[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
}

func fakeLocals(ips ...net.IP) func() ([]net.IP, error) {
	return func() ([]net.IP, error) { return ips, nil }
}

func fakeHostname(name string) func() (string, error) {
	return func() (string, error) { return name, nil }
}

func resolveForTest(t *testing.T, input string, lookup map[string][]net.IP, locals ...net.IP) *ListenAddress {
	t.Helper()
	resolved, err := resolveListenAddress(input, fakeLookup(lookup), fakeLocals(locals...), fakeHostname("myhost"))
	if err != nil {
		t.Fatalf("resolve %q: %v", input, err)
	}
	return resolved
}

func TestResolveListenAddress(t *testing.T) {
	loopbacks := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	tests := []struct {
		name     string
		input    string
		lookup   map[string][]net.IP
		locals   []net.IP
		loopback bool
		wildcard bool
		exposed  bool
	}{
		{name: "default loopback", input: "127.0.0.1:4646", loopback: true},
		{name: "ipv6 loopback", input: "[::1]:4646", loopback: true},
		{name: "localhost hostname", input: "localhost:4646", lookup: map[string][]net.IP{"localhost": loopbacks}, loopback: true},
		{name: "lan ipv4", input: "192.168.1.10:4646", locals: []net.IP{testLANIPv4}, exposed: true},
		{name: "lan ipv6", input: "[fd00::10]:4646", locals: []net.IP{testLANIPv6}, exposed: true},
		{name: "ipv4 wildcard", input: "0.0.0.0:4646", locals: []net.IP{testLANIPv4}, wildcard: true, exposed: true},
		{name: "ipv6 wildcard", input: "[::]:4646", locals: []net.IP{testLANIPv4, testLANIPv6}, wildcard: true, exposed: true},
		{name: "hostname to lan ip", input: "myhost:4646", lookup: map[string][]net.IP{"myhost": {testLANIPv4}}, locals: []net.IP{testLANIPv4}, exposed: true},
		{name: "hostname to loopback", input: "myhost:4646", lookup: map[string][]net.IP{"myhost": loopbacks}, loopback: true},
		{name: "hostname mixed loopback and lan", input: "myhost:4646", lookup: map[string][]net.IP{"myhost": {net.ParseIP("127.0.0.1"), testLANIPv4}}, locals: []net.IP{testLANIPv4}, exposed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved := resolveForTest(t, test.input, test.lookup, test.locals...)
			if resolved.Loopback() != test.loopback {
				t.Fatalf("%s: loopback = %v, want %v", test.input, resolved.Loopback(), test.loopback)
			}
			if resolved.wildcard != test.wildcard {
				t.Fatalf("%s: wildcard = %v, want %v", test.input, resolved.wildcard, test.wildcard)
			}
			if resolved.Exposed() != test.exposed {
				t.Fatalf("%s: exposed = %v, want %v", test.input, resolved.Exposed(), test.exposed)
			}
			if resolved.Port() != 4646 {
				t.Fatalf("%s: port = %d", test.input, resolved.Port())
			}
		})
	}
}

func TestResolveListenAddressErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		lookup  map[string][]net.IP
		locals  []net.IP
		message string
	}{
		{name: "empty", input: "", message: "cannot be empty"},
		{name: "missing port", input: "127.0.0.1", message: "invalid listen address"},
		{name: "empty host", input: ":4646", message: "host is empty"},
		{name: "non numeric port", input: "127.0.0.1:abc", message: "must be a number"},
		{name: "port zero", input: "127.0.0.1:0", message: "between 1 and 65535"},
		{name: "port too large", input: "127.0.0.1:65536", message: "between 1 and 65535"},
		{name: "ipv6 without brackets", input: "::1:4646", message: "brackets"},
		{name: "ip not local", input: "192.168.1.10:4646", message: "not assigned to a network interface"},
		{name: "unresolvable hostname", input: "ghost:4646", message: "cannot resolve host"},
		{name: "hostname resolves remote", input: "remote:4646", lookup: map[string][]net.IP{"remote": {net.ParseIP("8.8.8.8")}}, message: "not a local interface address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveListenAddress(test.input, fakeLookup(test.lookup), fakeLocals(test.locals...), fakeHostname("myhost"))
			if err == nil {
				t.Fatalf("%s should fail", test.input)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("%s: error %q should mention %q", test.input, err, test.message)
			}
		})
	}
}

func TestListenAddressEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		lookup   map[string][]net.IP
		locals   []net.IP
		endpoint string
	}{
		{input: "127.0.0.1:4646", endpoint: "http://127.0.0.1:4646"},
		{input: "[::1]:4646", endpoint: "http://[::1]:4646"},
		{input: "192.168.1.10:4646", locals: []net.IP{testLANIPv4}, endpoint: "http://192.168.1.10:4646"},
		{input: "[fd00::10]:4646", locals: []net.IP{testLANIPv6}, endpoint: "http://[fd00::10]:4646"},
		{input: "0.0.0.0:4646", locals: []net.IP{testLANIPv4}, endpoint: "http://127.0.0.1:4646"},
		{input: "[::]:4646", locals: []net.IP{testLANIPv4, testLANIPv6}, endpoint: "http://[::1]:4646"},
		{input: "myhost:4646", lookup: map[string][]net.IP{"myhost": {testLANIPv4}}, locals: []net.IP{testLANIPv4}, endpoint: "http://myhost:4646"},
	}
	for _, test := range tests {
		resolved := resolveForTest(t, test.input, test.lookup, test.locals...)
		if resolved.Endpoint() != test.endpoint {
			t.Fatalf("%s: endpoint = %s, want %s", test.input, resolved.Endpoint(), test.endpoint)
		}
	}
}

func TestListenAddressAllowsHost(t *testing.T) {
	lan := resolveForTest(t, "192.168.1.10:4646", nil, testLANIPv4)
	for host, want := range map[string]bool{
		"192.168.1.10:4646": true,
		"192.168.1.10":      true,
		"192.168.1.11:4646": false,
		"127.0.0.1:4646":    true,
		"[::1]:4646":        true,
		"localhost:4646":    true,
		"LOCALHOST:4646":    true,
		"myhost:4646":       true,
		"MYHOST.:4646":      true,
		"evil.example:4646": false,
		"[fd00::10]:4646":   false,
		"":                  false,
	} {
		if got := lan.AllowsHost(host); got != want {
			t.Fatalf("LAN listener AllowsHost(%q) = %v, want %v", host, got, want)
		}
	}

	wildcard := resolveForTest(t, "0.0.0.0:4646", nil, testLANIPv4, net.ParseIP("10.0.0.5"))
	for host, want := range map[string]bool{
		"192.168.1.10:4646": true,
		"10.0.0.5:4646":     true,
		"10.0.0.6:4646":     false,
		"myhost:4646":       true,
		"127.0.0.1:4646":    true,
	} {
		if got := wildcard.AllowsHost(host); got != want {
			t.Fatalf("wildcard listener AllowsHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestResolveDefaultListenAddressIsLoopback(t *testing.T) {
	resolved, err := ResolveListenAddress(DefaultListenAddress)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Loopback() || resolved.Exposed() {
		t.Fatalf("default address must stay loopback-only: %+v", resolved)
	}
	if resolved.BindAddress() != DefaultListenAddress {
		t.Fatalf("bind address = %s", resolved.BindAddress())
	}
}

func TestResolveListenAddressLocalhost(t *testing.T) {
	resolved, err := ResolveListenAddress("localhost:4646")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Loopback() {
		t.Fatalf("localhost must resolve to loopback: %+v", resolved)
	}
}

func TestResolveListenAddressOwnHostname(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip(err)
	}
	resolved, err := ResolveListenAddress(hostname + ":4646")
	if err != nil {
		t.Skipf("hostname %q does not resolve to a local interface here: %v", hostname, err)
	}
	if resolved.AllowsHost(hostname+":4646") != true {
		t.Fatalf("own hostname should be an allowed Host")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func localLANIPv4(t *testing.T) net.IP {
	t.Helper()
	ips, err := localInterfaceIPs()
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && !ip.IsLoopback() {
			return ip4
		}
	}
	t.Skip("no non-loopback IPv4 interface available")
	return nil
}

func TestServeOnLANAddress(t *testing.T) {
	ip := localLANIPv4(t)
	address := fmt.Sprintf("%s:%d", ip, freePort(t))
	resolved, err := ResolveListenAddress(address)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Exposed() {
		t.Fatalf("LAN address should be marked exposed")
	}
	listener, err := net.Listen("tcp", resolved.BindAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: New(store, "test", time.Now(), Dependencies{Listen: resolved}).Handler()}
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Close()

	response, err := http.Get("http://" + listener.Addr().String() + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status via LAN interface: %s", response.Status)
	}
}

func TestServeDefaultStaysLoopback(t *testing.T) {
	resolved, err := ResolveListenAddress(DefaultListenAddress)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", resolved.BindAddress())
	if err != nil {
		t.Skipf("default port unavailable: %v", err)
	}
	defer listener.Close()
	if ip := listener.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
		t.Fatalf("default listener must bind loopback, got %s", ip)
	}
}
