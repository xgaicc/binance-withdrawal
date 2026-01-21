package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDestination_Validate(t *testing.T) {
	tests := []struct {
		name    string
		dest    Destination
		wantErr bool
	}{
		{
			name:    "valid destination",
			dest:    Destination{Address: "7xKpR3gD...", Network: "SOL"},
			wantErr: false,
		},
		{
			name:    "missing address",
			dest:    Destination{Network: "SOL"},
			wantErr: true,
		},
		{
			name:    "missing network",
			dest:    Destination{Address: "7xKpR3gD..."},
			wantErr: true,
		},
		{
			name:    "empty destination",
			dest:    Destination{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dest.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadFromBytes(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		check   func(*testing.T, *DestinationConfig)
	}{
		{
			name: "valid configuration",
			yaml: `
destinations:
  midas-prod@example.com:
    SOL:
      address: "7xKpR3gD..."
      network: "SOL"
    ETH:
      address: "0xAbCdEf..."
      network: "ETH"
  midas-staging@example.com:
    SOL:
      address: "8yLqS4hE..."
      network: "SOL"
`,
			wantErr: false,
			check: func(t *testing.T, cfg *DestinationConfig) {
				dest, err := cfg.GetDestination("midas-prod@example.com", "SOL")
				if err != nil {
					t.Errorf("GetDestination() error = %v", err)
					return
				}
				if dest.Address != "7xKpR3gD..." {
					t.Errorf("Address = %q, want %q", dest.Address, "7xKpR3gD...")
				}
				if dest.Network != "SOL" {
					t.Errorf("Network = %q, want %q", dest.Network, "SOL")
				}

				// Check count
				if cfg.Count() != 3 {
					t.Errorf("Count() = %d, want 3", cfg.Count())
				}
			},
		},
		{
			name: "empty destinations section",
			yaml: `
destinations:
`,
			wantErr: false,
			check: func(t *testing.T, cfg *DestinationConfig) {
				if cfg.Count() != 0 {
					t.Errorf("Count() = %d, want 0", cfg.Count())
				}
			},
		},
		{
			name: "no destinations key",
			yaml: `
other_config: value
`,
			wantErr: false,
			check: func(t *testing.T, cfg *DestinationConfig) {
				if cfg.Count() != 0 {
					t.Errorf("Count() = %d, want 0", cfg.Count())
				}
			},
		},
		{
			name: "missing address",
			yaml: `
destinations:
  test@example.com:
    SOL:
      network: "SOL"
`,
			wantErr: true,
		},
		{
			name: "missing network",
			yaml: `
destinations:
  test@example.com:
    SOL:
      address: "7xKpR3gD..."
`,
			wantErr: true,
		},
		{
			name: "empty sub-account",
			yaml: `
destinations:
  "":
    SOL:
      address: "7xKpR3gD..."
      network: "SOL"
`,
			wantErr: true,
		},
		{
			name: "empty asset",
			yaml: `
destinations:
  test@example.com:
    "":
      address: "7xKpR3gD..."
      network: "SOL"
`,
			wantErr: true,
		},
		{
			name:    "invalid yaml",
			yaml:    "this is not: valid: yaml: [",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromBytes([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadFromBytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil && cfg != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	// Create a temp directory for test files
	tmpDir := t.TempDir()

	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "config.yaml")
		content := `
destinations:
  test@example.com:
    BTC:
      address: "bc1q..."
      network: "BTC"
`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		cfg, err := LoadFromFile(path)
		if err != nil {
			t.Fatalf("LoadFromFile() error = %v", err)
		}

		dest, err := cfg.GetDestination("test@example.com", "BTC")
		if err != nil {
			t.Fatalf("GetDestination() error = %v", err)
		}
		if dest.Address != "bc1q..." {
			t.Errorf("Address = %q, want %q", dest.Address, "bc1q...")
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := LoadFromFile(filepath.Join(tmpDir, "nonexistent.yaml"))
		if err == nil {
			t.Error("LoadFromFile() expected error for nonexistent file")
		}
	})
}

func TestDestinationConfig_GetDestination(t *testing.T) {
	cfg, err := LoadFromBytes([]byte(`
destinations:
  midas-prod@example.com:
    SOL:
      address: "7xKpR3gD..."
      network: "SOL"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	tests := []struct {
		name       string
		subAccount string
		asset      string
		wantAddr   string
		wantErr    bool
		errIs      error
	}{
		{
			name:       "existing destination",
			subAccount: "midas-prod@example.com",
			asset:      "SOL",
			wantAddr:   "7xKpR3gD...",
			wantErr:    false,
		},
		{
			name:       "unknown sub-account",
			subAccount: "unknown@example.com",
			asset:      "SOL",
			wantErr:    true,
			errIs:      ErrDestinationNotFound,
		},
		{
			name:       "unknown asset",
			subAccount: "midas-prod@example.com",
			asset:      "BTC",
			wantErr:    true,
			errIs:      ErrDestinationNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest, err := cfg.GetDestination(tt.subAccount, tt.asset)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDestination() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.errIs != nil && !errors.Is(err, tt.errIs) {
				t.Errorf("GetDestination() error = %v, want errors.Is %v", err, tt.errIs)
			}
			if !tt.wantErr && dest.Address != tt.wantAddr {
				t.Errorf("Address = %q, want %q", dest.Address, tt.wantAddr)
			}
		})
	}
}

func TestDestinationConfig_HasDestination(t *testing.T) {
	cfg, err := LoadFromBytes([]byte(`
destinations:
  test@example.com:
    ETH:
      address: "0x..."
      network: "ETH"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	if !cfg.HasDestination("test@example.com", "ETH") {
		t.Error("HasDestination() = false, want true for existing destination")
	}
	if cfg.HasDestination("test@example.com", "BTC") {
		t.Error("HasDestination() = true, want false for nonexistent asset")
	}
	if cfg.HasDestination("other@example.com", "ETH") {
		t.Error("HasDestination() = true, want false for nonexistent sub-account")
	}
}

func TestDestinationConfig_SetDestination(t *testing.T) {
	cfg := NewDestinationConfig()

	// Add a new destination
	err := cfg.SetDestination("test@example.com", "SOL", Destination{
		Address: "7xKpR3gD...",
		Network: "SOL",
	})
	if err != nil {
		t.Fatalf("SetDestination() error = %v", err)
	}

	// Verify it was added
	dest, err := cfg.GetDestination("test@example.com", "SOL")
	if err != nil {
		t.Fatalf("GetDestination() error = %v", err)
	}
	if dest.Address != "7xKpR3gD..." {
		t.Errorf("Address = %q, want %q", dest.Address, "7xKpR3gD...")
	}

	// Update existing destination
	err = cfg.SetDestination("test@example.com", "SOL", Destination{
		Address: "8yLqS4hE...",
		Network: "SOL",
	})
	if err != nil {
		t.Fatalf("SetDestination() error = %v", err)
	}

	dest, _ = cfg.GetDestination("test@example.com", "SOL")
	if dest.Address != "8yLqS4hE..." {
		t.Errorf("Address = %q, want %q after update", dest.Address, "8yLqS4hE...")
	}

	// Invalid destination
	err = cfg.SetDestination("test@example.com", "ETH", Destination{})
	if err == nil {
		t.Error("SetDestination() expected error for invalid destination")
	}
}

func TestDestinationConfig_Reload(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")

	// Write initial config
	initial := `
destinations:
  test@example.com:
    SOL:
      address: "initial..."
      network: "SOL"
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatalf("Failed to write initial config: %v", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}

	// Verify initial value
	dest, _ := cfg.GetDestination("test@example.com", "SOL")
	if dest.Address != "initial..." {
		t.Errorf("Initial Address = %q, want %q", dest.Address, "initial...")
	}

	// Write new config
	updated := `
destinations:
  test@example.com:
    SOL:
      address: "updated..."
      network: "SOL"
    ETH:
      address: "0xnew..."
      network: "ETH"
`
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	// Reload
	if err := cfg.Reload(path); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	// Verify updated values
	dest, _ = cfg.GetDestination("test@example.com", "SOL")
	if dest.Address != "updated..." {
		t.Errorf("Updated Address = %q, want %q", dest.Address, "updated...")
	}

	// Verify new asset
	dest, err = cfg.GetDestination("test@example.com", "ETH")
	if err != nil {
		t.Fatalf("GetDestination() for new asset error = %v", err)
	}
	if dest.Address != "0xnew..." {
		t.Errorf("New ETH Address = %q, want %q", dest.Address, "0xnew...")
	}
}

func TestDestinationConfig_ReloadFromBytes(t *testing.T) {
	cfg, err := LoadFromBytes([]byte(`
destinations:
  test@example.com:
    SOL:
      address: "initial..."
      network: "SOL"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	// Reload with new config
	err = cfg.ReloadFromBytes([]byte(`
destinations:
  other@example.com:
    BTC:
      address: "bc1q..."
      network: "BTC"
`))
	if err != nil {
		t.Fatalf("ReloadFromBytes() error = %v", err)
	}

	// Old destination should be gone
	if cfg.HasDestination("test@example.com", "SOL") {
		t.Error("Old destination should be removed after reload")
	}

	// New destination should exist
	if !cfg.HasDestination("other@example.com", "BTC") {
		t.Error("New destination should exist after reload")
	}
}

func TestDestinationConfig_SubAccounts(t *testing.T) {
	cfg, err := LoadFromBytes([]byte(`
destinations:
  alice@example.com:
    SOL:
      address: "..."
      network: "SOL"
  bob@example.com:
    ETH:
      address: "..."
      network: "ETH"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	accounts := cfg.SubAccounts()
	if len(accounts) != 2 {
		t.Errorf("SubAccounts() len = %d, want 2", len(accounts))
	}

	// Check both accounts are present (order not guaranteed)
	found := make(map[string]bool)
	for _, a := range accounts {
		found[a] = true
	}
	if !found["alice@example.com"] || !found["bob@example.com"] {
		t.Errorf("SubAccounts() = %v, want alice and bob", accounts)
	}
}

func TestDestinationConfig_Assets(t *testing.T) {
	cfg, err := LoadFromBytes([]byte(`
destinations:
  test@example.com:
    SOL:
      address: "..."
      network: "SOL"
    ETH:
      address: "..."
      network: "ETH"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	assets := cfg.Assets("test@example.com")
	if len(assets) != 2 {
		t.Errorf("Assets() len = %d, want 2", len(assets))
	}

	// Unknown sub-account
	assets = cfg.Assets("unknown@example.com")
	if assets != nil {
		t.Errorf("Assets() for unknown = %v, want nil", assets)
	}
}

func TestDestinationConfig_Concurrent(t *testing.T) {
	cfg, err := LoadFromBytes([]byte(`
destinations:
  test@example.com:
    SOL:
      address: "initial..."
      network: "SOL"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	var wg sync.WaitGroup
	const goroutines = 100

	// Concurrent reads
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cfg.GetDestination("test@example.com", "SOL")
				cfg.HasDestination("test@example.com", "SOL")
				cfg.SubAccounts()
				cfg.Assets("test@example.com")
				cfg.Count()
			}
		}()
	}

	// Concurrent writes
	for i := 0; i < goroutines/10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				cfg.SetDestination("test@example.com", "SOL", Destination{
					Address: "updated...",
					Network: "SOL",
				})
			}
		}(i)
	}

	// Concurrent reloads
	for i := 0; i < goroutines/10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg.ReloadFromBytes([]byte(`
destinations:
  test@example.com:
    SOL:
      address: "reloaded..."
      network: "SOL"
`))
		}()
	}

	wg.Wait()
}

func TestDestinationConfig_Count(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		count int
	}{
		{
			name:  "empty",
			yaml:  "destinations:",
			count: 0,
		},
		{
			name: "one sub-account one asset",
			yaml: `
destinations:
  test@example.com:
    SOL:
      address: "..."
      network: "SOL"
`,
			count: 1,
		},
		{
			name: "one sub-account two assets",
			yaml: `
destinations:
  test@example.com:
    SOL:
      address: "..."
      network: "SOL"
    ETH:
      address: "..."
      network: "ETH"
`,
			count: 2,
		},
		{
			name: "two sub-accounts multiple assets",
			yaml: `
destinations:
  alice@example.com:
    SOL:
      address: "..."
      network: "SOL"
    ETH:
      address: "..."
      network: "ETH"
  bob@example.com:
    BTC:
      address: "..."
      network: "BTC"
`,
			count: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromBytes([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("LoadFromBytes() error = %v", err)
			}
			if got := cfg.Count(); got != tt.count {
				t.Errorf("Count() = %d, want %d", got, tt.count)
			}
		})
	}
}
