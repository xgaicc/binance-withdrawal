// Package config provides configuration management for the withdrawal service.
package config

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrDestinationNotFound is returned when no destination is configured for a sub-account/asset combination.
var ErrDestinationNotFound = errors.New("destination not configured")

// Destination represents an external wallet address and network for withdrawals.
type Destination struct {
	Address string `yaml:"address" json:"address"`
	Network string `yaml:"network" json:"network"`
}

// Validate checks if the destination has required fields.
func (d *Destination) Validate() error {
	if d.Address == "" {
		return errors.New("destination address is required")
	}
	if d.Network == "" {
		return errors.New("destination network is required")
	}
	return nil
}

// DestinationConfig holds the mapping of sub-account email → asset → destination.
// Thread-safe for concurrent reads and supports hot reload.
type DestinationConfig struct {
	mu sync.RWMutex

	// destinations maps: subAccountEmail -> asset -> Destination
	destinations map[string]map[string]Destination
}

// NewDestinationConfig creates an empty DestinationConfig.
func NewDestinationConfig() *DestinationConfig {
	return &DestinationConfig{
		destinations: make(map[string]map[string]Destination),
	}
}

// LoadFromFile loads destination configuration from a YAML file.
// The file format is:
//
//	destinations:
//	  sub-account@example.com:
//	    SOL:
//	      address: "..."
//	      network: "SOL"
//	    ETH:
//	      address: "0x..."
//	      network: "ETH"
func LoadFromFile(path string) (*DestinationConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	return LoadFromBytes(data)
}

// LoadFromBytes loads destination configuration from YAML bytes.
func LoadFromBytes(data []byte) (*DestinationConfig, error) {
	// Parse the outer structure that contains destinations
	var raw struct {
		Destinations map[string]map[string]Destination `yaml:"destinations"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	cfg := NewDestinationConfig()
	if raw.Destinations == nil {
		return cfg, nil
	}

	// Validate all destinations
	for subAccount, assets := range raw.Destinations {
		if subAccount == "" {
			return nil, errors.New("empty sub-account email in configuration")
		}
		for asset, dest := range assets {
			if asset == "" {
				return nil, fmt.Errorf("empty asset for sub-account %q", subAccount)
			}
			if err := dest.Validate(); err != nil {
				return nil, fmt.Errorf("invalid destination for %s/%s: %w", subAccount, asset, err)
			}
		}
	}

	cfg.destinations = raw.Destinations
	return cfg, nil
}

// GetDestination returns the destination for the given sub-account and asset.
// Returns ErrDestinationNotFound if not configured.
func (c *DestinationConfig) GetDestination(subAccount, asset string) (*Destination, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	assets, ok := c.destinations[subAccount]
	if !ok {
		return nil, fmt.Errorf("sub-account %q: %w", subAccount, ErrDestinationNotFound)
	}

	dest, ok := assets[asset]
	if !ok {
		return nil, fmt.Errorf("asset %q for sub-account %q: %w", asset, subAccount, ErrDestinationNotFound)
	}

	return &dest, nil
}

// HasDestination checks if a destination is configured for the given sub-account and asset.
func (c *DestinationConfig) HasDestination(subAccount, asset string) bool {
	_, err := c.GetDestination(subAccount, asset)
	return err == nil
}

// SetDestination adds or updates a destination mapping.
// This method is thread-safe and supports hot reload scenarios.
func (c *DestinationConfig) SetDestination(subAccount, asset string, dest Destination) error {
	if err := dest.Validate(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.destinations[subAccount] == nil {
		c.destinations[subAccount] = make(map[string]Destination)
	}
	c.destinations[subAccount][asset] = dest
	return nil
}

// Reload replaces the entire configuration from a YAML file.
// This is atomic and thread-safe for hot reload support.
func (c *DestinationConfig) Reload(path string) error {
	newCfg, err := LoadFromFile(path)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.destinations = newCfg.destinations
	return nil
}

// ReloadFromBytes replaces the entire configuration from YAML bytes.
// This is atomic and thread-safe for hot reload support.
func (c *DestinationConfig) ReloadFromBytes(data []byte) error {
	newCfg, err := LoadFromBytes(data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.destinations = newCfg.destinations
	return nil
}

// SubAccounts returns a list of all configured sub-account emails.
func (c *DestinationConfig) SubAccounts() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	accounts := make([]string, 0, len(c.destinations))
	for subAccount := range c.destinations {
		accounts = append(accounts, subAccount)
	}
	return accounts
}

// Assets returns a list of all configured assets for a sub-account.
// Returns nil if the sub-account is not configured.
func (c *DestinationConfig) Assets(subAccount string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	assets, ok := c.destinations[subAccount]
	if !ok {
		return nil
	}

	result := make([]string, 0, len(assets))
	for asset := range assets {
		result = append(result, asset)
	}
	return result
}

// Count returns the total number of destination mappings configured.
func (c *DestinationConfig) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	for _, assets := range c.destinations {
		count += len(assets)
	}
	return count
}
