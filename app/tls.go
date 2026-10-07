package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

const tlsProfileDir = "/etc/opt/srlinux/tls"

// loadTLSConfig reads the SR Linux TLS profile files for name.
// <name>.pem and <name>.key.pem are the certificate and key.
// <name>.ca.pem is the trust anchor, when that file exists.
func loadTLSConfig(name string) (*tls.Config, error) {
	certPath := filepath.Join(tlsProfileDir, name+".pem")
	keyPath := filepath.Join(tlsProfileDir, name+".key.pem")
	caPath := filepath.Join(tlsProfileDir, name+".ca.pem")

	cfg := &tls.Config{}
	if fileExists(certPath) && fileExists(keyPath) {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("tls profile %s: %w", name, err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if fileExists(caPath) {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("tls profile %s: %w", name, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls profile %s: no certificates in %s", name, caPath)
		}
		cfg.RootCAs = pool
		cfg.ClientCAs = pool
	}
	if len(cfg.Certificates) == 0 && cfg.RootCAs == nil {
		return nil, fmt.Errorf("tls profile %s: no certificate or trust anchor in %s", name, tlsProfileDir)
	}
	return cfg, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
