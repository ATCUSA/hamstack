package config

import "fmt"

// NetworkAccess returns the current site's override, then the machine default.
func (c Config) NetworkAccess(target, fallback string) string {
	if s, ok := c.Here(); ok && s.Network[target] != "" {
		return s.Network[target]
	}
	if value := c.Network[target]; value != "" {
		return value
	}
	return fallback
}
func (c *Config) SetNetworkAccess(target, mode string) error {
	access, err := ParseAccess(mode)
	if err != nil {
		return err
	}
	if target == "" {
		return fmt.Errorf("network target is required")
	}
	if s := c.siteRef(c.CurrentSite); s != nil {
		if s.Network == nil {
			s.Network = map[string]string{}
		}
		s.Network[target] = access
		return nil
	}
	if c.Network == nil {
		c.Network = map[string]string{}
	}
	c.Network[target] = access
	return nil
}

// BindHost resolves a network preference using the current addresses. Without a LAN
// address it fails closed to loopback, rather than exposing the app on all interfaces.
func BindHost(mode string, lan []string) string {
	switch mode {
	case AccessAll:
		return "0.0.0.0"
	case AccessNetwork:
		if len(lan) > 0 {
			return lan[0]
		}
	}
	return "127.0.0.1"
}
