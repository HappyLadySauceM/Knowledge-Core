package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func generateCertificates(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create fixture certificate directory: %w", err)
	}
	now := time.Now().UTC()
	caSerial, err := certificateSerial()
	if err != nil {
		return err
	}
	caCertificate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "Knowledge Core interop test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate fixture CA key: %w", err)
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caCertificate, caCertificate, caPublic, caPrivate)
	if err != nil {
		return fmt.Errorf("create fixture CA certificate: %w", err)
	}
	if err := writePEM(filepath.Join(directory, "ca.pem"), "CERTIFICATE", caDER, 0o644); err != nil {
		return err
	}
	if err := createLeaf(directory, "server", caCertificate, caPrivate, now, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}); err != nil {
		return err
	}
	if err := createLeaf(directory, "client", caCertificate, caPrivate, now, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}); err != nil {
		return err
	}
	fmt.Printf("CERTS_READY %s\n", directory)
	return nil
}

func createLeaf(
	directory string,
	name string,
	caCertificate *x509.Certificate,
	caPrivate ed25519.PrivateKey,
	now time.Time,
	usage []x509.ExtKeyUsage,
) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate fixture %s key: %w", name, err)
	}
	serial, err := certificateSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Knowledge Core interop " + name},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usage,
	}
	if name == "server" {
		template.DNSNames = []string{"rust-server", "go-server", "localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, caCertificate, public, caPrivate)
	if err != nil {
		return fmt.Errorf("create fixture %s certificate: %w", name, err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return fmt.Errorf("marshal fixture %s private key: %w", name, err)
	}
	if err := writePEM(filepath.Join(directory, name+".pem"), "CERTIFICATE", certificateDER, 0o644); err != nil {
		return err
	}
	return writePEM(filepath.Join(directory, name+"-key.pem"), "PRIVATE KEY", privateDER, 0o600)
}

func writePEM(path, blockType string, bytes []byte, mode os.FileMode) error {
	contents := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes})
	if err := os.WriteFile(path, contents, mode); err != nil {
		return fmt.Errorf("write fixture certificate %q: %w", path, err)
	}
	return nil
}

func certificateSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate fixture certificate serial: %w", err)
	}
	return serial, nil
}
