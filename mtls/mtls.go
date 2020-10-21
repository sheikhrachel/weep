package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/spf13/viper"

	"path/filepath"
	"runtime"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/netflix/weep/config"
	"github.com/netflix/weep/util"
	log "github.com/sirupsen/logrus"
)

// getTLSConfig makes and returns a pointer to a tls.Config
func getTLSConfig() (*tls.Config, error) {
	dirs, err := getTLSDirs()
	if err != nil {
		return nil, err
	}
	certFile, keyFile, caFile, insecure, err := getClientCertificatePaths(dirs)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := makeTLSConfig(certFile, keyFile, caFile, insecure)
	if err != nil {
		return nil, err
	}
	return tlsConfig, nil
}

func makeTLSConfig(certFile, keyFile, caFile string, insecure bool) (*tls.Config, error) {
	if certFile == "" || keyFile == "" || caFile == "" {
		log.Error("MTLS cert, key, or CA file not defined in configuration")
		return nil, MissingTLSConfigError
	}
	caCert, _ := ioutil.ReadFile(caFile)
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: insecure,
		RootCAs:            caCertPool,
		Certificates:       []tls.Certificate{cert},
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			// Based on the golang verification code. See https://golang.org/src/crypto/tls/handshake_client.go
			certs := make([]*x509.Certificate, len(rawCerts))
			for i, asn1Data := range rawCerts {
				cert, err := x509.ParseCertificate(asn1Data)
				if err != nil {
					return fmt.Errorf("tls: failed to parse certificate from server: %w", err)
				}
				certs[i] = cert
			}

			opts := x509.VerifyOptions{
				Roots:         caCertPool,
				DNSName:       "",
				Intermediates: x509.NewCertPool(),
			}

			for i, cert := range certs {
				if i == 0 {
					continue
				}
				opts.Intermediates.AddCert(cert)
			}
			verifiedChains, err := certs[0].Verify(opts)
			if err != nil {
				return err
			}
			return nil
		},
	}
	return tlsConfig, nil
}

func NewHTTPClient() (*http.Client, error) {
	tlsConfig, err := getTLSConfig()
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return client, nil
}

// getTLSDirs returns a list of directories to search for mTLS certs based on platform
func getTLSDirs() ([]string, error) {
	// Select config section based on platform
	mtlsDirKey := fmt.Sprintf("mtls_settings.%s", runtime.GOOS)
	mtlsDirs := viper.GetStringSlice(mtlsDirKey)

	// Replace $HOME token with home dir
	homeDir, err := homedir.Dir()
	if err != nil {
		return nil, HomeDirectoryError
	}
	for i, path := range mtlsDirs {
		mtlsDirs[i] = strings.Replace(path, "$HOME", homeDir, -1)
	}
	return mtlsDirs, nil
}

func getClientCertificatePaths(configDirs []string) (string, string, string, bool, error) {
	// If cert, key, and catrust are paths that exist, we'll just use those
	cert := viper.GetString("mtls_settings.cert")
	key := viper.GetString("mtls_settings.key")
	caFile := viper.GetString("mtls_settings.catrust")
	insecure := viper.GetBool("mtls_settings.insecure")
	if util.FileExists(cert) && util.FileExists(key) && util.FileExists(caFile) {
		return cert, key, caFile, insecure, nil
	}

	// Otherwise, look for the files in the list of dirs from the config
	for _, metatronDir := range configDirs {
		certPath := filepath.Join(metatronDir, cert)
		if !util.FileExists(certPath) {
			continue
		}

		keyPath := filepath.Join(metatronDir, key)
		if !util.FileExists(keyPath) {
			continue
		}

		caPath := filepath.Join(metatronDir, caFile)
		if !util.FileExists(caPath) {
			continue
		}

		return certPath, keyPath, caPath, insecure, nil
	}
	return "", "", "", false, config.ClientCertificatesNotFoundError
}
