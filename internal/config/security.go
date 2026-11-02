package config

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// SecurityValues carries non-secret transport configuration across input sources.
type SecurityValues struct {
	APITokenPath        string `yaml:"apiTokenPath"`
	APITLSCertFile      string `yaml:"apiTLSCertFile"`
	APITLSKeyFile       string `yaml:"apiTLSKeyFile"`
	Production          bool   `yaml:"production"`
	NATSCredentialsFile string `yaml:"natsCredentialsFile"`
	NATSTLSCAFile       string `yaml:"natsTLSCAFile"`
	NATSTLSCertFile     string `yaml:"natsTLSCertFile"`
	NATSTLSKeyFile      string `yaml:"natsTLSKeyFile"`
	NATSTLSServerName   string `yaml:"natsTLSServerName"`
	productionSet       bool
}

func securityValuesFromConfig(c Config) SecurityValues {
	return SecurityValues{APITokenPath: c.APITokenPath, APITLSCertFile: c.APITLSCertFile, APITLSKeyFile: c.APITLSKeyFile, Production: c.Production, NATSCredentialsFile: c.NATSCredentialsFile, NATSTLSCAFile: c.NATSTLSCAFile, NATSTLSCertFile: c.NATSTLSCertFile, NATSTLSKeyFile: c.NATSTLSKeyFile, NATSTLSServerName: c.NATSTLSServerName, productionSet: true}
}

func mergeSecurityValues(base, override SecurityValues) SecurityValues {
	pairs := [][2]*string{{&base.APITokenPath, &override.APITokenPath}, {&base.APITLSCertFile, &override.APITLSCertFile}, {&base.APITLSKeyFile, &override.APITLSKeyFile}, {&base.NATSCredentialsFile, &override.NATSCredentialsFile}, {&base.NATSTLSCAFile, &override.NATSTLSCAFile}, {&base.NATSTLSCertFile, &override.NATSTLSCertFile}, {&base.NATSTLSKeyFile, &override.NATSTLSKeyFile}, {&base.NATSTLSServerName, &override.NATSTLSServerName}}
	for _, pair := range pairs {
		if strings.TrimSpace(*pair[1]) != "" {
			*pair[0] = strings.TrimSpace(*pair[1])
		}
	}
	if override.productionSet || override.Production {
		base.Production = override.Production
		base.productionSet = true
	}
	return base
}

func resolveSecurityPaths(c *Config) error {
	for _, p := range []*string{&c.APITokenPath, &c.APITLSCertFile, &c.APITLSKeyFile, &c.NATSCredentialsFile, &c.NATSTLSCAFile, &c.NATSTLSCertFile, &c.NATSTLSKeyFile} {
		v, err := expandPath(*p)
		if err != nil {
			return err
		}
		*p = v
	}
	if c.APITokenPath == "" {
		c.APITokenPath = filepath.Join(c.DataDir, "api.token")
	}
	return nil
}

// ValidateAPIListener confines management to loopback, with optional local TLS.
func ValidateAPIListener(addr, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return errors.New("API TLS certificate and key must both be configured")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("local API address must be host:port")
	}
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if !loopback {
		return errors.New("local API address must use a loopback host")
	}
	return nil
}

func ValidateSecurity(c Config) error {
	for _, id := range c.AgentAllowedIDs {
		if id == "" || len(id) > 128 {
			return errors.New("agent allowed IDs must be 1-128 ASCII letters, digits, underscores or hyphens")
		}
		for _, ch := range id {
			if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
				return errors.New("agent allowed IDs must be 1-128 ASCII letters, digits, underscores or hyphens")
			}
		}
	}
	// Empty addresses occur in partial configuration validation; the server always validates its actual listener.
	if c.LocalAPIAddr != "" {
		if err := ValidateAPIListener(c.LocalAPIAddr, c.APITLSCertFile, c.APITLSKeyFile); err != nil {
			return err
		}
	}
	if (c.NATSTLSCertFile == "") != (c.NATSTLSKeyFile == "") {
		return errors.New("NATS TLS client certificate and key must both be configured")
	}
	if !c.Production {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(c.TrustMode), "open") {
		return errors.New("production forbids open trust mode")
	}
	if c.NATSCredentialsFile == "" && c.NATSTLSCertFile == "" {
		return errors.New("production requires NATS identity credentials or a TLS client certificate")
	}
	for _, server := range c.NATSServers {
		u, err := url.Parse(server)
		if err != nil || (u.Scheme != "tls" && u.Scheme != "wss") {
			return errors.New("production requires TLS NATS server URLs")
		}
		if u.User != nil {
			return errors.New("NATS credentials must not be embedded in server URLs")
		}
	}
	return nil
}
