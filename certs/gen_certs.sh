#!/usr/bin/env bash
# =============================================================================
# gen_certs.sh — Generate self-signed TLS certificates for local mTLS testing
# =============================================================================
# This script creates:
#   ca.crt / ca.key       → The Certificate Authority that signs all other certs
#   gateway.crt / .key    → The Gateway's client certificate (sent to backends)
#   server.crt / .key     → An example backend server certificate
#
# Usage:
#   cd certs && chmod +x gen_certs.sh && ./gen_certs.sh
# =============================================================================

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "🔐 Generating Certificate Authority (CA)..."
openssl genrsa -out ca.key 4096
openssl req -new -x509 -days 3650 -key ca.key -out ca.crt \
  -subj "/C=IN/ST=Maharashtra/L=Mumbai/O=API Gateway/CN=api-gateway-ca"

echo "✅ CA generated: ca.crt, ca.key"
echo ""

# ── Gateway Client Certificate ──────────────────────────────────────────────
# This cert is presented by the Gateway when connecting to backends.
# Backends configured for mTLS will reject any connection that doesn't
# present a cert signed by our CA.
echo "🔐 Generating Gateway client certificate..."
openssl genrsa -out gateway.key 2048
openssl req -new -key gateway.key -out gateway.csr \
  -subj "/C=IN/ST=Maharashtra/L=Mumbai/O=API Gateway/CN=api-gateway-client"
openssl x509 -req -days 365 -in gateway.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out gateway.crt -sha256
rm -f gateway.csr
echo "✅ Gateway client cert generated: gateway.crt, gateway.key"
echo ""

# ── Mock Backend Server Certificate ─────────────────────────────────────────
# This simulates a backend service's TLS certificate.
# The gateway verifies this against ca.crt before trusting the connection.
echo "🔐 Generating mock backend server certificate..."
openssl genrsa -out server.key 2048
openssl req -new -key server.key -out server.csr \
  -subj "/C=IN/ST=Maharashtra/L=Mumbai/O=Backend Service/CN=localhost"
openssl x509 -req -days 365 -in server.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out server.crt -sha256 \
  -extfile <(echo "subjectAltName=DNS:localhost,IP:127.0.0.1")
rm -f server.csr
echo "✅ Backend server cert generated: server.crt, server.key"
echo ""

echo "============================================"
echo "🎉 All certificates generated successfully!"
echo "============================================"
echo ""
echo "  Update config/config.yaml:"
echo "  tls:"
echo "    enabled: true"
echo "    ca_cert:     certs/ca.crt"
echo "    client_cert: certs/gateway.crt"
echo "    client_key:  certs/gateway.key"
echo ""
echo "  Backend servers must be configured to:"
echo "    1. Serve with server.crt / server.key"
echo "    2. Require client certs validated against ca.crt"
