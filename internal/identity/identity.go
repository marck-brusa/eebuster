// Package identity loads or generates the persistent EEBUS identity certificate for the
// primary stack. Generation must never overwrite existing key material -- the SKI derived
// from the keypair is how EEBUS peers identify us, and replacing it silently invalidates
// every existing trust relationship.
package identity

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/enbility/ship-go/cert"
)

// LoadOrCreate reads certPath/keyPath if both exist, otherwise generates a fresh identity and
// persists it to those paths before returning it. dir is created if missing.
//
// The second return value reports whether a new identity was generated. Callers must surface
// that: a new keypair means a new SKI, and every device already paired with the previous one
// will refuse the new one until it is paired again. Pointing -data-dir at a fresh directory is
// enough to trigger it, which makes it easy to do by accident.
func LoadOrCreate(certPath, keyPath, vendorCode, model, country, serial string) (tls.Certificate, bool, error) {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
			return certificate, false, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return tls.Certificate{}, false, fmt.Errorf("identity: creating %s: %w", filepath.Dir(certPath), err)
	}

	certificate, err := cert.CreateCertificate(vendorCode, model, country, serial)
	if err != nil {
		return tls.Certificate{}, false, fmt.Errorf("identity: generating certificate: %w", err)
	}
	if err := writeKey(certificate, keyPath); err != nil {
		return tls.Certificate{}, false, fmt.Errorf("identity: writing key: %w", err)
	}
	if err := writeCertificate(certificate, certPath); err != nil {
		return tls.Certificate{}, false, fmt.Errorf("identity: writing certificate: %w", err)
	}
	return certificate, true, nil
}

// SKI returns the EEBUS SKI of a loaded identity: the SubjectKeyIdentifier of its leaf
// certificate, lowercase hex, which is the value peers use to identify us.
func SKI(certificate tls.Certificate) (string, error) {
	if len(certificate.Certificate) == 0 {
		return "", errors.New("identity: certificate carries no leaf")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("identity: parsing leaf certificate: %w", err)
	}
	ski, err := cert.SkiFromCertificate(leaf)
	if err != nil {
		return "", fmt.Errorf("identity: reading ski: %w", err)
	}
	return strings.ToLower(ski), nil
}

// AnnouncedSerial folds a fragment of the SKI into the configured serial number.
//
// Identity is anchored on the SKI, but a device's paired-device list shows the announced
// brand/model/serial and the SHIP id derived from them -- not usually the SKI. Two identities
// built from the same config file therefore appear as two rows identical in every visible
// field, and picking the wrong one silently pairs something that is not running. Diagnosed
// against real hardware, where five pairing attempts all landed on an orphaned identity and the
// device reported success every time.
//
// A fragment is enough to tell them apart, and keeps the serial recognisable. Pairing is
// unaffected: SHIP trust is keyed on the SKI, so changing the announced serial does not
// invalidate an existing pairing, it only relabels it. Returns serial unchanged if the SKI is
// missing or too short to slice.
func AnnouncedSerial(serial, ski string) string {
	const fragment = 8
	if len(ski) < fragment {
		return serial
	}
	return serial + "-" + strings.ToLower(ski[:fragment])
}

func writeKey(certificate tls.Certificate, path string) error {
	key, ok := certificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("unexpected private key type %T", certificate.PrivateKey)
	}
	bytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return pem.Encode(file, &pem.Block{Type: "EC PRIVATE KEY", Bytes: bytes})
}

func writeCertificate(certificate tls.Certificate, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, leaf := range certificate.Certificate {
		if err := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: leaf}); err != nil {
			return err
		}
	}
	return nil
}
