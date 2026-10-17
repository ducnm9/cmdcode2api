package app

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
)

// Version is the program version, formatted as vX.Y.Z.
const Version = "v0.2.0"

// configFile is a var so tests can redirect config persistence to a temp dir.
var configFile = "config.yaml"

func Run() {
	oauthMode := flag.Bool("oauth", false, "obtain a Command Code API key via browser OAuth")
	oauthCallback := flag.String("oauth-callback", "", "OAuth callback URL, e.g. http://server.example.com:5959/callback")
	host := flag.String("host", "", "HTTP listen host, e.g. localhost or 0.0.0.0")
	port := flag.Int("port", 0, "HTTP listen port")
	debug := flag.Bool("debug", false, "print request body and all CC SSE events to stderr")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("cmdcode2api %s (go %s)\n", Version, runtime.Version())
		os.Exit(0)
	}

	cfgPath := findConfig()

	// --oauth mode: log in via the browser to obtain an API key
	if *oauthMode {
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			log.Fatalf("load config failed: %v", err)
		}
		if cfg == nil {
			// no config yet, generate one first
			cfg2, err := defaultConfig()
			if err != nil {
				log.Fatalf("create config failed: %v", err)
			}
			if err := writeConfigTemplate(cfgPath, cfg2); err != nil {
				log.Fatalf("create config failed: %v", err)
			}
			cfg = cfg2
		}

		cb, err := runOAuth(OAuthOptions{CallbackURL: *oauthCallback})
		if err != nil {
			log.Fatalf("OAuth failed: %v", err)
		}

		// OAuth appends an account instead of overwriting: run it repeatedly to connect multiple accounts.
		pool := NewAccountPool(cfg.CommandCode.Accounts)
		if acct := pool.Get(accountID(cb.APIKey)); acct != nil {
			fmt.Printf("\nℹ️  API key already configured as account %q in %s\n", acct.Name, cfgPath)
			return
		}
		name := cb.displayName()
		if name == "" {
			name = oauthAccountName(pool)
		}
		if _, err := pool.Add(name, cb.APIKey, true); err != nil {
			log.Fatalf("add oauth account failed: %v", err)
		}
		pool.SyncToConfig(cfg)
		if err := saveConfig(cfgPath, cfg); err != nil {
			log.Fatalf("save config failed: %v", err)
		}

		fmt.Printf("\n✅ API key added as account %q in %s (%d account(s) total)\n", name, cfgPath, pool.Len())
		fmt.Println("   You can now run cmdcode2api to start the server.")
		return
	}

	// normal mode
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	// first run — generate config
	if cfg == nil {
		cfg2, err := defaultConfig()
		if err != nil {
			log.Fatalf("create config failed: %v", err)
		}
		if err := writeConfigTemplate(cfgPath, cfg2); err != nil {
			log.Fatalf("create config failed: %v", err)
		}
		fmt.Printf(`cmdcode2api initialized.

Created config: %s
Local client key: %s
WebUI admin password: %s

Next:
  1. Run ./cmdcode2api --oauth to connect Command Code.
  2. Run ./cmdcode2api again to start the local OpenAI-compatible API.

Alternatively, just start the server — it comes up without an account, and
you can add one in the WebUI at /webui with the admin password above.

Use the local client key above as the Bearer token for your OpenAI client.
`, cfgPath, cfg2.APIKeys[0].Key, cfg2.AdminPassword)
		os.Exit(0)
	}

	// The server still starts with no upstream accounts: WebUI/client keys/settings all work,
	// and chat requests return 503 no_accounts until an account is added via the WebUI.
	if len(cfg.CommandCode.Accounts) == 0 {
		log.Printf("[WARN] no Command Code accounts configured; chat requests will return 503 until an account is added via the WebUI (/webui) or --oauth")
	}

	// Generate a WebUI admin password when empty, printed only once.
	adminPasswordGenerated := false
	if cfg.adminPassword() == "" {
		password, err := genAdminPassword()
		if err != nil {
			log.Fatalf("generate admin password failed: %v", err)
		}
		cfg.setAdminPassword(password)
		if err := saveConfig(cfgPath, cfg); err != nil {
			log.Fatalf("save config failed: %v", err)
		}
		adminPasswordGenerated = true
	}

	if cfg.Port == 0 {
		cfg.Port = 11434
	}
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	if *host != "" {
		cfg.Host = *host
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if cfg.UpstreamBaseURL() == "" {
		cfg.SetUpstreamBaseURL("https://api.commandcode.ai")
	}
	if *debug {
		cfg.Debug = true
		debugMode = true
	}

	// Mirror logs into a ring buffer so the WebUI can display them.
	ring := newLogRing()
	log.SetOutput(io.MultiWriter(os.Stderr, ring))

	pool := NewAccountPool(cfg.CommandCode.Accounts)
	cc := NewCCClientWithPool(pool, cfg.UpstreamBaseURL())
	usage := loadUsage()

	if primary := pool.Primary(); primary != nil {
		FetchProviderModels(cfg.UpstreamBaseURL(), primary.APIKey)
	} else {
		log.Printf("[WARN] no enabled Command Code accounts; starting with an empty model catalog")
	}

	log.Printf("accounts: %d configured, %d enabled", pool.Len(), pool.EnabledCount())
	if adminPasswordGenerated {
		fmt.Printf("WebUI admin password generated: %s\n", cfg.adminPassword())
	}

	if err := runServer(cc, cfg, usage, ring); err != nil {
		log.Fatalf("server failed: %v", err)
	}
	if err := usage.save(); err != nil {
		log.Printf("save usage failed: %v", err)
	}
}

// oauthAccountName derives a label for an OAuth-added account.
func oauthAccountName(pool *AccountPool) string {
	return fmt.Sprintf("oauth-%d", pool.Len()+1)
}

func findConfig() string {
	return configFile
}
