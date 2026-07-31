package kitex

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestTLSDialerCompletesMutualTLSHandshake(t *testing.T) {
	certificates := newTestCertificates(t)
	serverConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificates.server},
		ClientCAs:    certificates.roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	clientConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      certificates.roots,
		ServerName:   "identity.test",
		Certificates: []tls.Certificate{certificates.client},
	}

	clientErr, serverErr := runTLSHandshake(t, serverConfig, clientConfig)
	if clientErr != nil || serverErr != nil {
		t.Fatalf("mutual TLS handshake errors: client=%v server=%v", clientErr, serverErr)
	}
}

func TestTLSDialerRejectsClientWithoutRequiredCertificate(t *testing.T) {
	certificates := newTestCertificates(t)
	serverConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificates.server},
		ClientCAs:    certificates.roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	clientConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    certificates.roots,
		ServerName: "identity.test",
	}

	clientErr, serverErr := runTLSHandshake(t, serverConfig, clientConfig)
	if clientErr == nil && serverErr == nil {
		t.Fatal("mutual TLS handshake succeeded without a client certificate")
	}
}

func runTLSHandshake(t *testing.T, serverConfig, clientConfig *tls.Config) (error, error) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	listener := tls.NewListener(raw, serverConfig)
	defer func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			t.Errorf("listener.Close() error = %v", closeErr)
		}
	}()
	serverResult := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverResult <- acceptErr
			return
		}
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		handshakeErr := connection.(*tls.Conn).Handshake()
		serverResult <- errors.Join(handshakeErr, connection.Close())
	}()

	connection, clientErr := (tlsDialer{config: clientConfig}).DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if connection != nil {
		clientErr = errors.Join(clientErr, connection.Close())
	}
	return clientErr, <-serverResult
}

type testCertificates struct {
	roots  *x509.CertPool
	server tls.Certificate
	client tls.Certificate
}

func newTestCertificates(t *testing.T) testCertificates {
	t.Helper()
	now := time.Now().UTC()
	caKey := newPrivateKey(t)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Knowledge Core test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(CA) error = %v", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("ParseCertificate(CA) error = %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)

	return testCertificates{
		roots: roots,
		server: signedCertificate(t, ca, caKey, &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject:      pkix.Name{CommonName: "identity.test"},
			DNSNames:     []string{"identity.test"},
			NotBefore:    now.Add(-time.Minute),
			NotAfter:     now.Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}),
		client: signedCertificate(t, ca, caKey, &x509.Certificate{
			SerialNumber: big.NewInt(3),
			Subject:      pkix.Name{CommonName: "knowledge-core-client"},
			NotBefore:    now.Add(-time.Minute),
			NotAfter:     now.Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}),
	}
}

func signedCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, template *x509.Certificate) tls.Certificate {
	t.Helper()
	key := newPrivateKey(t)
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(%s) error = %v", template.Subject.CommonName, err)
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatalf("ParseCertificate(%s) error = %v", template.Subject.CommonName, err)
	}
	return tls.Certificate{
		Certificate: [][]byte{certificateDER, ca.Raw},
		PrivateKey:  key,
		Leaf:        certificate,
	}
}

func newPrivateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return key
}
