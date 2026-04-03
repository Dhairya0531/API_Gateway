package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// NewMTLSTransport builds an http.RoundTripper that enforces mutual TLS.
//
// How mTLS works:
//   - Standard TLS: client verifies server cert → only the server authenticates.
//   - Mutual TLS:   BOTH sides present certificates.
//     The server verifies the gateway's client cert before accepting the connection.
//     This means only our gateway can talk to the backends — no rogue processes allowed.
//
// Parameters:
//   - caCertPath:   Path to the CA certificate. The gateway uses this to verify
//     that the backend server's certificate was signed by a trusted authority.
//   - clientCertPath: Path to the gateway's own certificate (presented to the backend).
//   - clientKeyPath:  Path to the gateway's private key (used to prove cert ownership).
func NewMTLSTransport(caCertPath, clientCertPath, clientKeyPath string) (http.RoundTripper, error) {
	// 1. Load the gateway's own client certificate + private key
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("loading gateway client cert: %w", err)
	}

	// 2. Load the CA certificate pool.
	// The gateway will ONLY trust backend servers whose certificate was signed by this CA.
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", caCertPath)
	}

	// 3. Build the TLS configuration
	tlsCfg := &tls.Config{
		// Present our client certificate to backend servers
		Certificates: []tls.Certificate{clientCert},
		// Verify backend server certs against our CA pool
		RootCAs: caCertPool,
		// Enforce TLS 1.3 minimum — TLS 1.2 and earlier have known vulnerabilities
		MinVersion: tls.VersionTLS13,
	}

	// 4. Wrap in a standard HTTP transport
	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
		// Keep reasonable connection pool settings
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
	}

	return transport, nil
}
