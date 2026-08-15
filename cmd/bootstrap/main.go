// Command bootstrap performs the local, one-time first-administrator ceremony.
// It never listens on a network port and deliberately accepts the password and
// authenticator code through files, not environment variables or argv.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/netcore-isp/netcore/internal/bootstrap"
	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/database"
	"github.com/netcore-isp/netcore/internal/secrets"
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	tenantName := flag.String("tenant-name", "", "legal or operating name of the first tenant")
	tenantSlug := flag.String("tenant-slug", "", "lowercase URL-safe tenant slug")
	timezone := flag.String("timezone", "", "IANA tenant timezone, for example Africa/Lagos")
	currency := flag.String("currency", "", "three-letter currency, for example NGN")
	email := flag.String("email", "", "first administrator email address")
	passwordFile := flag.String("password-file", "", "absolute path to one-time first administrator password file")
	totpSecretRef := flag.String("totp-secret-ref", "", "logical SecretStore reference for the initial administrator TOTP secret")
	totpCodeFile := flag.String("totp-code-file", "", "absolute path to one-time current authenticator-code file")
	flag.Parse()

	password, err := readOneTimeFile(*passwordFile, "password")
	if err != nil {
		return err
	}
	code, err := readOneTimeFile(*totpCodeFile, "authenticator code")
	if err != nil {
		return err
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if !cfg.Auth.RequireMFA {
		return errors.New("NETCORE_AUTH_REQUIRE_MFA must be true for first administrator bootstrap")
	}
	secretStore, err := secrets.NewSOPSFileStore(cfg.Secrets.Ref)
	if err != nil {
		return fmt.Errorf("bootstrap SecretStore: %w", err)
	}
	hasher, err := argon2id.New(argon2id.Params{
		Memory:      cfg.Auth.PasswordMemoryKiB,
		Iterations:  cfg.Auth.PasswordIterations,
		Parallelism: cfg.Auth.PasswordParallelism,
		SaltLength:  16,
		KeyLength:   32,
	})
	if err != nil {
		return fmt.Errorf("password hashing configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.ConnectTimeout)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	store, err := bootstrap.NewPostgresStore(db)
	if err != nil {
		return err
	}
	service, err := bootstrap.NewService(store, hasher, secretStore)
	if err != nil {
		return err
	}
	result, err := service.Run(ctx, bootstrap.Input{
		TenantName: *tenantName, TenantSlug: *tenantSlug, Timezone: *timezone, Currency: *currency,
		Email: *email, Password: password, TOTPSecretRef: *totpSecretRef, TOTPCode: code,
	})
	if err != nil {
		return err
	}
	fmt.Printf("first administrator created for tenant %q (tenant %s, user %s)\n", *tenantSlug, result.TenantID, result.UserID)
	return nil
}

func readOneTimeFile(path, label string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s file path must be absolute", label)
	}
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read %s file: %w", label, err)
	}
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "", fmt.Errorf("%s file is empty", label)
	}
	return value, nil
}
